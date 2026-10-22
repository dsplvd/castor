package ffmpeg

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// Encoder is one concrete way to produce a codec: the ffmpeg -c:v name plus its command fragments.
type Encoder struct {
	Name     string       // -c:v value, e.g. "libx264", "hevc_videotoolbox"
	Codec    media.Codec  // the abstract codec produced, independent of the name
	Hardware bool         // GPU-backed: trusted only after a real test encode
	InitArgs []string     // emitted before the input: hardware device setup
	Filters  []string     // appended to the -vf chain: e.g. the GPU upload
	Flags    []string     // encoder-specific -c:v flags: preset, pix_fmt, GOP
	GPU      *GPUPipeline // how frames stay on the GPU from decode to encode; nil where the encoder has no such path
}

// GPUPipeline is a hardware encoder's way of keeping a picture on the GPU from decode to encode, so the CPU never touches it.
type GPUPipeline struct {
	InputArgs []string // input options for the video input: hardware decode into GPU frames
	Scale     string   // the filter that resizes those frames
	// Codecs are the source codecs this host has proven it can decode, scale and encode that way.
	// It is empty until proven: an encoder that merely exists says nothing about what its GPU decodes.
	Codecs []media.Codec
}

// DecodesOnGPU reports whether a source of codec, at bitDepth, can take the GPU path on this host.
func (e Encoder) DecodesOnGPU(codec media.Codec, bitDepth int) bool {
	// Only 8-bit is proven: a GPU that decodes a codec does not necessarily decode its 10-bit profiles.
	return e.GPU != nil && bitDepth == 8 && slices.Contains(e.GPU.Codecs, codec)
}

const vaapiRenderNode = "/dev/dri/renderD128"

// pix_fmt is fixed on CPU encoders; VA-API converts on the GPU instead.
var (
	// veryfast keeps the software encoders ahead of realtime in the live pipeline.
	softwareFlags     = []string{"-preset", "veryfast", "-pix_fmt", "yuv420p"}
	videotoolboxFlags = []string{"-pix_fmt", "yuv420p", "-g", "600"}
	vaapiFilters      = []string{"format=nv12", "hwupload"}
)

var (
	libx264 = Encoder{Name: "libx264", Codec: media.CodecH264, Flags: softwareFlags}
	libx265 = Encoder{Name: "libx265", Codec: media.CodecHEVC, Flags: slices.Concat(softwareFlags, []string{"-x265-params", "log-level=error"})}

	// VideoToolbox can emit SEI that its FFmpeg wrapper cannot parse when it
	// merges embedded A53 captions. Drop that caption side data on re-encode
	// instead of aborting the video stream.
	h264VideoToolbox = Encoder{Name: "h264_videotoolbox", Codec: media.CodecH264, Hardware: true, Flags: slices.Concat(videotoolboxFlags, []string{"-a53cc", "0"})}
	hevcVideoToolbox = Encoder{Name: "hevc_videotoolbox", Codec: media.CodecHEVC, Hardware: true, Flags: videotoolboxFlags}
	h264NVENC        = Encoder{Name: "h264_nvenc", Codec: media.CodecH264, Hardware: true, Flags: []string{"-preset", "p4", "-pix_fmt", "yuv420p"}}
	hevcNVENC        = Encoder{Name: "hevc_nvenc", Codec: media.CodecHEVC, Hardware: true, Flags: []string{"-preset", "p4", "-pix_fmt", "yuv420p"}}
	h264QSV          = Encoder{Name: "h264_qsv", Codec: media.CodecH264, Hardware: true, Flags: []string{"-preset", "veryfast", "-pix_fmt", "nv12"}}
	hevcQSV          = Encoder{Name: "hevc_qsv", Codec: media.CodecHEVC, Hardware: true, Flags: []string{"-preset", "veryfast", "-pix_fmt", "nv12"}}
	h264AMF          = Encoder{Name: "h264_amf", Codec: media.CodecH264, Hardware: true, Flags: []string{"-quality", "speed", "-pix_fmt", "nv12"}}
	hevcAMF          = Encoder{Name: "hevc_amf", Codec: media.CodecHEVC, Hardware: true, Flags: []string{"-quality", "speed", "-pix_fmt", "nv12"}}

	h264VAAPI = vaapiEncoder("h264_vaapi", media.CodecH264, vaapiRenderNode)
	hevcVAAPI = vaapiEncoder("hevc_vaapi", media.CodecHEVC, vaapiRenderNode)
)

// encoders is every encoder castor knows, grouped by codec, hardware before software.
var encoders = []Encoder{
	h264VideoToolbox, h264NVENC, h264QSV, h264VAAPI, h264AMF, libx264,
	hevcVideoToolbox, hevcNVENC, hevcQSV, hevcVAAPI, hevcAMF, libx265,
}

// candidates is encoders plus VA-API on every other render node this host has.
func candidates() []Encoder {
	out := slices.Clone(encoders)
	nodes, _ := filepath.Glob("/dev/dri/renderD*")
	for _, node := range nodes {
		if node == vaapiRenderNode {
			continue
		}
		out = append(out, vaapiEncoder("h264_vaapi", media.CodecH264, node), vaapiEncoder("hevc_vaapi", media.CodecHEVC, node))
	}
	return out
}

func vaapiEncoder(name string, codec media.Codec, node string) Encoder {
	const alias = "va"
	return Encoder{
		Name: name, Codec: codec, Hardware: true,
		InitArgs: []string{"-init_hw_device", "vaapi=" + alias + ":" + node, "-filter_hw_device", alias},
		Filters:  vaapiFilters,
		GPU: &GPUPipeline{
			InputArgs: []string{"-hwaccel", "vaapi", "-hwaccel_device", alias, "-hwaccel_output_format", "vaapi"},
			Scale:     "scale_vaapi",
		},
	}
}

// Encoders is the best working encoder per codec on ffmpegPath, hardware first; each encoder is proven once by the value it returns.
func Encoders(ffmpegPath string) func(context.Context, media.Codec) (Encoder, bool) {
	proven := &provenEncoders{ffmpegPath: ffmpegPath, slots: map[string]*proof{}}
	return proven.best
}

type provenEncoders struct {
	ffmpegPath string

	mu    sync.Mutex
	slots map[string]*proof
}

// proof is one encoder's test, held per encoder so a slow test blocks only askers of that same encoder.
type proof struct {
	mu     sync.Mutex
	tested bool
	works  bool

	// gpuTested and gpuCodecs are the GPU pipeline's own test, run only after the encoder itself works.
	gpuTested bool
	gpuCodecs []media.Codec
}

func (p *provenEncoders) best(ctx context.Context, codec media.Codec) (Encoder, bool) {
	all := candidates()
	for _, hardware := range []bool{true, false} {
		for _, e := range all {
			if e.Codec != codec || e.Hardware != hardware {
				continue
			}
			works, ran := p.available(ctx, e)
			if !works {
				continue
			}
			if ran {
				slog.InfoContext(ctx, "encoder selected", "encoder", e.Name, "codec", string(codec), "hardware", hardware)
			}
			return p.withGPU(ctx, e), true
		}
	}
	return Encoder{}, false
}

// available reports whether e works, and whether this call ran the test encode that proved it.
func (p *provenEncoders) available(ctx context.Context, e Encoder) (works, ran bool) {
	slot := p.slot(e)
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if slot.tested {
		return slot.works, false
	}
	works = testEncode(ctx, p.ffmpegPath, e)
	// A cancelled test proves nothing, and remembering it would disable working hardware.
	if ctx.Err() == nil {
		slot.tested, slot.works = true, works
	}
	if !works {
		slog.InfoContext(ctx, "encoder unavailable", "encoder", e.Name, "hardware", e.Hardware)
	}
	return works, true
}

// slot is e's test record, made on first ask.
func (p *provenEncoders) slot(e Encoder) *proof {
	key := strings.Join(slices.Concat([]string{e.Name}, e.InitArgs, e.Filters, e.Flags), "\x00")
	p.mu.Lock()
	defer p.mu.Unlock()
	slot := p.slots[key]
	if slot == nil {
		slot = &proof{}
		p.slots[key] = slot
	}
	return slot
}

// withGPU is e with the source codecs its GPU pipeline has proven it decodes, testing them once.
// A pipeline that cannot be proven stays empty, which sends every cast down the software decode path.
func (p *provenEncoders) withGPU(ctx context.Context, e Encoder) Encoder {
	if e.GPU == nil {
		return e
	}
	slot := p.slot(e)
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if !slot.gpuTested {
		codecs := testGPUPipeline(ctx, p.ffmpegPath, e)
		// A cancelled test proves nothing, and remembering it would disable working hardware.
		if ctx.Err() != nil {
			return e
		}
		slot.gpuTested, slot.gpuCodecs = true, codecs
		if len(codecs) > 0 {
			slog.InfoContext(ctx, "GPU decode proven", "encoder", e.Name, "source_codecs", codecs)
		} else {
			slog.InfoContext(ctx, "GPU decode unavailable, decoding on the CPU", "encoder", e.Name)
		}
	}
	gpu := *e.GPU
	gpu.Codecs = slices.Clone(slot.gpuCodecs)
	e.GPU = &gpu
	return e
}

// gpuSources are the source codecs a GPU pipeline is tried on, each with the software encoder that makes a clip of it.
var gpuSources = []struct {
	codec media.Codec
	clip  []string
}{
	{media.CodecH264, []string{"-c:v", "libx264", "-pix_fmt", "yuv420p"}},
	{media.CodecHEVC, []string{"-c:v", "libx265", "-pix_fmt", "yuv420p", "-x265-params", "log-level=error"}},
}

// testGPUPipeline returns the source codecs e decodes, scales and encodes without a frame leaving the GPU.
func testGPUPipeline(ctx context.Context, ffmpegPath string, e Encoder) []media.Codec {
	dir, err := os.MkdirTemp("", "castor-gpu-")
	if err != nil {
		return nil
	}
	defer func() { _ = os.RemoveAll(dir) }()

	var proven []media.Codec
	for _, source := range gpuSources {
		clip := filepath.Join(dir, string(source.codec)+".ts")
		if ctx.Err() != nil || !makeClip(ctx, ffmpegPath, clip, source.clip) {
			continue
		}
		if testDecode(ctx, ffmpegPath, e, clip) {
			proven = append(proven, source.codec)
		}
	}
	return proven
}

// makeClip writes a short software-encoded clip of one codec, the source the GPU is asked to decode.
func makeClip(ctx context.Context, ffmpegPath, path string, encode []string) bool {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	args := []string{"-hide_banner", "-f", "lavfi", "-i", "testsrc2=size=640x360:rate=25:duration=0.4"}
	args = append(args, encode...)
	args = append(args, "-f", "mpegts", path)
	_, err := Run(ctx, ffmpegPath, args, nil, nil)
	return err == nil
}

// testDecode runs the clip through e's whole GPU pipeline, scaling as a cast does.
func testDecode(ctx context.Context, ffmpegPath string, e Encoder, clip string) bool {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	args := append([]string{"-hide_banner"}, e.InitArgs...)
	args = append(args, e.GPU.InputArgs...)
	args = append(args, "-i", clip,
		"-vf", e.GPU.Scale+"=w='min(iw,320)':h='min(ih,180)':force_original_aspect_ratio=decrease:force_divisible_by=2:format=nv12",
		"-c:v", e.Name)
	args = append(args, e.Flags...)
	args = append(args, "-f", "null", "-")
	_, err := Run(ctx, ffmpegPath, args, nil, nil)
	return err == nil
}

// testEncode encodes one tenth of a second with e's own device setup and filters.
func testEncode(ctx context.Context, ffmpegPath string, e Encoder) bool {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	args := append([]string{"-hide_banner"}, e.InitArgs...)
	args = append(args, "-f", "lavfi", "-i", "testsrc2=size=256x144:rate=25:duration=0.1")
	if len(e.Filters) > 0 {
		args = append(args, "-vf", strings.Join(e.Filters, ","))
	}
	args = append(args, "-c:v", e.Name)
	args = append(args, e.Flags...)
	args = append(args, "-f", "null", "-")
	_, err := Run(ctx, ffmpegPath, args, nil, nil)
	return err == nil
}

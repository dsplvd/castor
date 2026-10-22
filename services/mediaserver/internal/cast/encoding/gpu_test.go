package encoding

import (
	"slices"
	"strings"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/codec"
	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding/fetch"
	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

var gpuInput = []string{"-hwaccel", "test", "-hwaccel_device", "va", "-hwaccel_output_format", "test"}

// gpuEncoder is a hardware encoder that has a GPU pipeline, as VA-API does once its decode is proven.
var gpuEncoder = ffmpeg.Encoder{
	Name: "h264_test_hw", Codec: media.CodecH264, Hardware: true,
	InitArgs: []string{"-init_hw_device", "test=host"},
	Filters:  []string{"format=nv12", "hwupload"},
	GPU:      &ffmpeg.GPUPipeline{InputArgs: gpuInput, Scale: "scale_test", Codecs: []media.Codec{media.CodecH264}},
}

func gpuVideo(mutate func(*codec.VideoEncode)) codec.Track[codec.VideoEncode] {
	venc := codec.VideoEncode{Encoder: gpuEncoder, GPUDecode: true, Bitrate: "4M", MaxHeight: 1080, MaxFrameRate: 60}
	if mutate != nil {
		mutate(&venc)
	}
	return codec.Encode(venc)
}

func TestAPlanThatDecodesOnTheGPUKeepsTheFramesThere(t *testing.T) {
	piped := FromPipe(fetch.Pace{})
	for _, tt := range []struct {
		name  string
		video codec.Track[codec.VideoEncode]
		vf    string
		gpu   bool
	}{{
		name:  "a height ceiling resizes on the GPU and never uploads",
		video: gpuVideo(nil),
		vf:    "fps='min(source_fps,60)',scale_test=w=-2:h='min(1080,max(2,trunc(ih/2)*2))':format=nv12",
		gpu:   true,
	}, {
		name:  "a width and height ceiling keeps the aspect ratio",
		video: gpuVideo(func(v *codec.VideoEncode) { v.MaxWidth = 1920 }),
		vf:    "fps='min(source_fps,60)',scale_test=w='min(iw,1920)':h='min(ih,1080)':force_original_aspect_ratio=decrease:force_divisible_by=2:format=nv12",
		gpu:   true,
	}, {
		name:  "no ceiling only fixes the format",
		video: gpuVideo(func(v *codec.VideoEncode) { v.MaxHeight, v.MaxFrameRate = 0, 0 }),
		vf:    "scale_test=format=nv12",
		gpu:   true,
	}, {
		name:  "an odd ceiling rounds down to an even height",
		video: gpuVideo(func(v *codec.VideoEncode) { v.MaxHeight = 719 }),
		vf:    "fps='min(source_fps,60)',scale_test=w=-2:h='min(718,max(2,trunc(ih/2)*2))':format=nv12",
		gpu:   true,
	}, {
		name:  "the same encoder without the GPU decode decodes on the CPU and uploads",
		video: gpuVideo(func(v *codec.VideoEncode) { v.GPUDecode = false }),
		vf:    "fps='min(source_fps,60)',scale=-2:'min(1080,max(2,trunc(ih/2)*2))',format=nv12,hwupload",
	}} {
		t.Run(tt.name, func(t *testing.T) {
			args := mustEncodeArgs(t, EncodeOptions{Input: piped, Format: mpegtsFormat, Audio: aacAudio, Video: tt.video})
			if got := argValue(args, "-vf"); got != tt.vf {
				t.Errorf("-vf = %q, want %q", got, tt.vf)
			}
			// The decode options belong to the input they precede, so they come right before it.
			want := append(slices.Clone(gpuInput), "-f", SpoolFormat.Muxer, "-i", ffmpeg.StdinPipe)
			if got := containsSequence(args, want); got != tt.gpu {
				t.Errorf("GPU decode options before the input = %v, want %v\nargs: %s", got, tt.gpu, strings.Join(args, " "))
			}
			if !tt.gpu {
				return
			}
			if strings.Contains(argValue(args, "-vf"), "hwupload") {
				t.Errorf("-vf uploads frames that are already on the GPU: %q", argValue(args, "-vf"))
			}
			if !containsSequence(args, []string{"-init_hw_device", "test=host"}) {
				t.Error("the hardware device is not opened for the decode to use")
			}
		})
	}
}

func TestAPullDecodesOnTheGPUToo(t *testing.T) {
	src := muxedSource(t, mustURL(t, "http://example.test/in.m3u8"), media.HLS, fetch.Policy{})
	args := mustPullArgs(t, PullOptions{Source: src, Audio: codec.CopyAudio(), Video: gpuVideo(nil)})
	if !containsSequence(args, append(slices.Clone(gpuInput), "-i", "http://example.test/in.m3u8")) {
		t.Errorf("the spool pull does not decode on the GPU: %s", strings.Join(args, " "))
	}
	if strings.Contains(argValue(args, "-vf"), "hwupload") {
		t.Errorf("the spool pull uploads GPU frames: %q", argValue(args, "-vf"))
	}
}

func TestOnlyTheInputThatCarriesThePictureDecodesOnTheGPU(t *testing.T) {
	const video, sound = "https://video.test/master.m3u8", "https://audio.test/sound.m3u8"
	for _, tt := range []struct {
		name   string
		inputs []string // input ids in program order; "picture" carries the video
	}{
		{"picture first", []string{"picture", "sound"}},
		{"sound first", []string{"sound", "picture"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			urls := map[media.InputID]string{"picture": video, "sound": sound}
			var inputs []media.Input
			for _, id := range tt.inputs {
				inputs = append(inputs, media.Input{ID: media.InputID(id), URL: mustURL(t, urls[media.InputID(id)]), ContentType: media.HLS})
			}
			program := mustProgram(t, media.Program{
				Inputs: inputs,
				Tracks: []media.TrackRef{
					{Input: "picture", Kind: media.TrackVideo},
					{Input: "sound", Kind: media.TrackAudio},
				},
				ClockInput: "picture", EndPolicy: media.EndAtLongest,
			})
			args := mustPullArgs(t, PullOptions{
				Source: mustProgramSource(t, program, fetch.Plan{"picture": {}, "sound": {}}),
				Audio:  codec.CopyAudio(), Video: gpuVideo(nil),
			})
			if n := strings.Count(strings.Join(args, " "), "-hwaccel_output_format"); n != 1 {
				t.Fatalf("the GPU decode options appear %d times, want once: %s", n, strings.Join(args, " "))
			}
			if !containsSequence(args, append(slices.Clone(gpuInput), "-i", video)) {
				t.Errorf("the picture input does not decode on the GPU: %s", strings.Join(args, " "))
			}
			if containsSequence(args, append(slices.Clone(gpuInput), "-i", sound)) {
				t.Errorf("the sound input was told to decode on the GPU: %s", strings.Join(args, " "))
			}
		})
	}
}

func TestACopyNeverTakesTheGPUPath(t *testing.T) {
	args := mustEncodeArgs(t, EncodeOptions{Input: FromPipe(fetch.Pace{}), Format: mpegtsFormat, Video: codec.CopyVideo(), Audio: aacAudio})
	if slices.Contains(args, "-hwaccel") {
		t.Errorf("a copy asked for hardware decode: %s", strings.Join(args, " "))
	}
}

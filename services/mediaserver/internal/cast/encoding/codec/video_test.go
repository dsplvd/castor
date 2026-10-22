package codec

import (
	"context"
	"slices"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

func hostEncoders(_ context.Context, codec media.Codec) (ffmpeg.Encoder, bool) {
	switch codec {
	case media.CodecHEVC:
		return ffmpeg.Encoder{Name: "hevc_videotoolbox", Codec: media.CodecHEVC, Hardware: true}, true
	case media.CodecH264:
		return ffmpeg.Encoder{Name: "libx264", Codec: media.CodecH264}, true
	}
	return ffmpeg.Encoder{}, false
}

func TestSelectVideoEncoder(t *testing.T) {
	decodes := func(codecs ...media.Codec) media.Capabilities {
		var r media.Capabilities
		for _, c := range codecs {
			r.Formats = append(r.Formats, media.FormatSupport{Transport: media.TransportMP4, Video: media.VideoSupport{Codec: c}})
		}
		return r
	}
	hevcHW := ffmpeg.Encoder{Name: "hevc_videotoolbox", Codec: media.CodecHEVC, Hardware: true}
	hevcSW := ffmpeg.Encoder{Name: "libx265", Codec: media.CodecHEVC}
	h264HW := ffmpeg.Encoder{Name: "h264_videotoolbox", Codec: media.CodecH264, Hardware: true}
	h264SW := ffmpeg.Encoder{Name: "libx264", Codec: media.CodecH264}

	for _, tt := range []struct {
		name  string
		caps  media.Capabilities
		avail map[media.Codec]ffmpeg.Encoder
		want  string
	}{
		{"hardware HEVC first", decodes(media.CodecHEVC, media.CodecH264), map[media.Codec]ffmpeg.Encoder{media.CodecHEVC: hevcHW, media.CodecH264: h264HW}, "hevc_videotoolbox"},
		{"software HEVC is never run live", decodes(media.CodecHEVC, media.CodecH264), map[media.Codec]ffmpeg.Encoder{media.CodecHEVC: hevcSW, media.CodecH264: h264HW}, "h264_videotoolbox"},
		{"only what the device decodes", decodes(media.CodecH264), map[media.Codec]ffmpeg.Encoder{media.CodecHEVC: hevcHW, media.CodecH264: h264SW}, "libx264"},
		{"nothing in common", decodes(media.CodecVP8), map[media.Codec]ffmpeg.Encoder{media.CodecH264: h264SW}, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, _, _ := selectVideoEncoder(tt.caps, DefaultRate.targets(), func(c media.Codec) (ffmpeg.Encoder, bool) { e, ok := tt.avail[c]; return e, ok })
			if got.Name != tt.want {
				t.Errorf("selectVideoEncoder = %q, want %q", got.Name, tt.want)
			}
		})
	}
}

func TestWhatForcesAVideoReEncode(t *testing.T) {
	h264 := media.Capabilities{Formats: []media.FormatSupport{{Transport: media.TransportMP4, Video: media.VideoSupport{Codec: media.CodecH264}}}}
	hdrCapable := media.Capabilities{Formats: []media.FormatSupport{{Transport: media.TransportMP4, Video: media.VideoSupport{Codec: media.CodecHEVC, Profiles: []media.Profile{"Main 10"}, BitDepths: []int{8, 10}}}, {Transport: media.TransportMP4, Video: media.VideoSupport{Codec: media.CodecH264}}}}
	sdr := media.ProbeInfo{VideoCodec: media.CodecH264, VideoHeight: 720, VideoBitDepth: 8}
	tall := media.ProbeInfo{VideoCodec: media.CodecH264, VideoHeight: 2160, VideoBitDepth: 8}
	hdr := media.ProbeInfo{VideoCodec: media.CodecHEVC, VideoProfile: "Main 10", VideoHeight: 720, VideoBitDepth: 10, VideoHDR: true}

	for _, tt := range []struct {
		name   string
		caps   media.Capabilities
		probe  media.ProbeInfo
		burnIn string
		want   []Reason
	}{
		{name: "nothing against it copies", caps: h264, probe: sdr},
		{name: "a source above the ceiling", caps: h264, probe: tall, want: []Reason{reasonHeightLimit}},
		{name: "HDR even on a device that decodes it", caps: hdrCapable, probe: hdr, want: []Reason{reasonHDRPolicy}},
		{name: "a burn-in needs decoded frames", caps: h264, probe: sdr, burnIn: "/tmp/cue.txt", want: []Reason{reasonSubtitleBurnIn}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			in := Inputs{Caps: tt.caps, Probe: tt.probe, Into: testFormat(t, media.MPEGTS), MaxHeight: 1080, BurnIn: tt.burnIn, Encoders: hostEncoders}
			track, refused, err := decideVideo(t.Context(), in)
			if err != nil {
				t.Fatalf("decideVideo: %v", err)
			}
			if !slices.Equal(refused, tt.want) {
				t.Errorf("reasons = %v, want %v", refused, tt.want)
			}
			if enc, ok := track.Encode(); ok && (enc.MaxHeight != in.MaxHeight || enc.SubtitleTextFile != tt.burnIn) {
				t.Errorf("re-encode carries ceiling %d and burn-in %q, want %d and %q", enc.MaxHeight, enc.SubtitleTextFile, in.MaxHeight, tt.burnIn)
			}
		})
	}
}

func TestWhatKeepsASourceOffTheGPU(t *testing.T) {
	proven := ffmpeg.Encoder{Name: "h264_vaapi", Codec: media.CodecH264, Hardware: true,
		GPU: &ffmpeg.GPUPipeline{Codecs: []media.Codec{media.CodecH264}}}
	unproven := ffmpeg.Encoder{Name: "h264_vaapi", Codec: media.CodecH264, Hardware: true, GPU: &ffmpeg.GPUPipeline{}}
	software := ffmpeg.Encoder{Name: "libx264", Codec: media.CodecH264}
	sdr := media.ProbeInfo{VideoCodec: media.CodecH264, VideoBitDepth: 8}

	for _, tt := range []struct {
		name       string
		enc        ffmpeg.Encoder
		probe      media.ProbeInfo
		burnIn     string
		interlaced bool
		want       bool
	}{
		{name: "a proven codec at 8 bits", enc: proven, probe: sdr, want: true},
		{name: "an encoder with no GPU pipeline", enc: software, probe: sdr},
		{name: "a pipeline that proved nothing", enc: unproven, probe: sdr},
		{name: "a codec the GPU was not proven to decode", enc: proven, probe: media.ProbeInfo{VideoCodec: media.CodecHEVC, VideoBitDepth: 8}},
		{name: "ten bits", enc: proven, probe: media.ProbeInfo{VideoCodec: media.CodecH264, VideoBitDepth: 10}},
		{name: "a bit depth the probe did not report", enc: proven, probe: media.ProbeInfo{VideoCodec: media.CodecH264}},
		{name: "a source of unknown codec", enc: proven, probe: media.ProbeInfo{VideoBitDepth: 8}},
		{name: "HDR needs the CPU tone map", enc: proven, probe: media.ProbeInfo{VideoCodec: media.CodecH264, VideoBitDepth: 8, VideoHDR: true}},
		{name: "a turned picture needs a CPU filter", enc: proven, probe: media.ProbeInfo{VideoCodec: media.CodecH264, VideoBitDepth: 8, VideoRotation: 90}},
		{name: "interlaced frames the device cannot weave", enc: proven, probe: sdr, interlaced: true},
		{name: "drawn subtitles need CPU frames", enc: proven, probe: sdr, burnIn: "/tmp/cue.txt"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := decodesOnGPU(tt.enc, Inputs{Probe: tt.probe, BurnIn: tt.burnIn}, tt.interlaced); got != tt.want {
				t.Errorf("decodesOnGPU = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTheVideoPlanTakesTheGPUPathOnlyWhenTheSourceQualifies(t *testing.T) {
	gpu := ffmpeg.Encoder{Name: "h264_vaapi", Codec: media.CodecH264, Hardware: true,
		GPU: &ffmpeg.GPUPipeline{Codecs: []media.Codec{media.CodecH264}}}
	encoders := func(_ context.Context, c media.Codec) (ffmpeg.Encoder, bool) { return gpu, c == media.CodecH264 }
	h264 := media.Capabilities{Formats: []media.FormatSupport{{Transport: media.TransportMP4, Video: media.VideoSupport{Codec: media.CodecH264}}}}

	for _, tt := range []struct {
		name   string
		probe  media.ProbeInfo
		burnIn string
		want   bool
	}{
		{"8-bit H.264", media.ProbeInfo{VideoCodec: media.CodecH264, VideoHeight: 1080, VideoBitDepth: 8}, "", true},
		{"10-bit H.264", media.ProbeInfo{VideoCodec: media.CodecH264, VideoHeight: 1080, VideoBitDepth: 10}, "", false},
		{"8-bit H.264 with a burn-in", media.ProbeInfo{VideoCodec: media.CodecH264, VideoHeight: 1080, VideoBitDepth: 8}, "/tmp/cue.txt", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// A spliced source cannot be copied, so the plan must re-encode it.
			in := Inputs{Caps: h264, Probe: tt.probe, Measured: true, Into: testFormat(t, media.MPEGTS), MaxHeight: 1080, Spliced: true, BurnIn: tt.burnIn, Encoders: encoders}
			track, _, err := decideVideo(t.Context(), in)
			if err != nil {
				t.Fatalf("decideVideo: %v", err)
			}
			enc, ok := track.Encode()
			if !ok {
				t.Fatal("the plan copies a spliced source")
			}
			if enc.GPUDecode != tt.want {
				t.Errorf("GPUDecode = %v, want %v", enc.GPUDecode, tt.want)
			}
		})
	}
}

// A server that copies live sources asks the plan as it asks any source: not spliced. What keeps that safe is that a copy
// which breaks is remembered, so the retry decodes the same packets instead of copying them again.
func TestACopyThatBrokeIsNotOfferedAgain(t *testing.T) {
	h264 := media.Capabilities{Formats: []media.FormatSupport{{Transport: media.TransportMP4, Video: media.VideoSupport{Codec: media.CodecH264}}}}
	probe := media.ProbeInfo{VideoCodec: media.CodecH264, VideoHeight: 1080, VideoBitDepth: 8}
	for _, tt := range []struct {
		name       string
		spliced    bool
		decode     media.Axes
		wantCopy   bool
		wantReason []Reason
	}{
		{name: "a live source trusted and unbroken is copied", wantCopy: true},
		{name: "the same source after its copy broke is re-encoded", decode: media.Axes{Video: true}, wantReason: []Reason{reasonVideoCopyFailed}},
		{name: "a source that declares its seams is never copied", spliced: true, wantReason: []Reason{reasonSpliced}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			in := Inputs{Caps: h264, Probe: probe, Measured: true, Into: testFormat(t, media.MPEGTS), MaxHeight: 1080, Spliced: tt.spliced, Decode: tt.decode, Encoders: hostEncoders}
			track, refused, err := decideVideo(t.Context(), in)
			if err != nil {
				t.Fatal(err)
			}
			if _, encoded := track.Encode(); encoded == tt.wantCopy {
				t.Errorf("re-encoded = %v, want a copy = %v", encoded, tt.wantCopy)
			}
			if !slices.Equal(refused, tt.wantReason) {
				t.Errorf("reasons = %v, want %v", refused, tt.wantReason)
			}
		})
	}
}

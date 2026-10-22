package codec

import (
	"context"
	"strings"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

func TestParseRate(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want string // the rate as ffmpeg spells it back, or "" for an error
	}{
		{"4M", "4M"},
		{"8M", "8M"},
		{"8m", "8M"},
		{" 8M ", "8M"},
		{"6000k", "6M"},
		{"6500k", "6500k"},
		{"1.5M", "1500k"},
		{"8000000", "8M"},
		{"1234567", "1234567"},
		{"500k", "500k"},
		{"40M", "40M"},
		{"", ""},
		{"fast", ""},
		{"8MB", ""},
		{"-4M", ""},
		{"8 M", ""},
		{"499999", ""},
		{"41M", ""},
		{"0", ""},
	} {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseRate(tt.in)
			if tt.want == "" {
				if err == nil {
					t.Fatalf("ParseRate(%q) = %v, want an error", tt.in, got)
				}
				return
			}
			if err != nil || got.String() != tt.want {
				t.Fatalf("ParseRate(%q) = %v, %v, want %s", tt.in, got, err, tt.want)
			}
		})
	}
}

func TestARateIsASetting(t *testing.T) {
	var r Rate
	if err := r.UnmarshalText([]byte("8M")); err != nil || r.String() != "8M" {
		t.Fatalf("UnmarshalText(8M) = %v, %v", r, err)
	}
	// An empty setting is no setting: the default.
	if err := r.UnmarshalText(nil); err != nil || r != (Rate{}) {
		t.Fatalf("UnmarshalText(empty) = %v, %v, want the zero rate", r, err)
	}
	if err := r.UnmarshalText([]byte("nonsense")); err == nil || !strings.Contains(err.Error(), "nonsense") {
		t.Errorf("UnmarshalText(nonsense) = %v, want an error naming the value", err)
	}
	if got := (Rate{}).String(); got != DefaultRate.String() || got != "4M" {
		t.Errorf("the zero rate spells %q, want the default 4M", got)
	}
}

func TestARateSetsEachCodecsTarget(t *testing.T) {
	for _, tt := range []struct {
		rate                 string
		h264                 [3]string // bitrate, maxrate, bufsize
		hevcBitrate, hevcBuf string
	}{
		{"", [3]string{"4M", "4M", "8M"}, "2M", "4M"},
		{"4M", [3]string{"4M", "4M", "8M"}, "2M", "4M"},
		{"8M", [3]string{"8M", "8M", "16M"}, "4M", "8M"},
		{"6000k", [3]string{"6M", "6M", "12M"}, "3M", "6M"},
		{"1.5M", [3]string{"1500k", "1500k", "3M"}, "750k", "1500k"},
	} {
		t.Run(tt.rate, func(t *testing.T) {
			var r Rate
			if tt.rate != "" {
				var err error
				if r, err = ParseRate(tt.rate); err != nil {
					t.Fatal(err)
				}
			}
			targets := r.targets()
			if len(targets) != 2 || targets[0].codec != media.CodecHEVC || targets[1].codec != media.CodecH264 {
				t.Fatalf("targets = %+v, want HEVC then H.264", targets)
			}
			if got := [3]string{targets[1].bitrate, targets[1].maxrate, targets[1].bufsize}; got != tt.h264 {
				t.Errorf("H.264 = %v, want %v", got, tt.h264)
			}
			if got := targets[0]; got.bitrate != tt.hevcBitrate || got.maxrate != tt.hevcBitrate || got.bufsize != tt.hevcBuf {
				t.Errorf("HEVC = %+v, want %s under a %s buffer", got, tt.hevcBitrate, tt.hevcBuf)
			}
		})
	}
}

// The default rate is what castor always encoded at, so a server that sets nothing plans exactly as before.
func TestTheDefaultRateIsTheOldTargets(t *testing.T) {
	targets := DefaultRate.targets()
	want := []videoTarget{
		{codec: media.CodecHEVC, bitrate: "2M", maxrate: "2M", bufsize: "4M"},
		{codec: media.CodecH264, bitrate: "4M", maxrate: "4M", bufsize: "8M"},
	}
	for i := range want {
		if targets[i] != want[i] {
			t.Errorf("target %d = %+v, want %+v", i, targets[i], want[i])
		}
	}
}

func TestTheRateReachesTheVideoPlans(t *testing.T) {
	r, err := ParseRate("8M")
	if err != nil {
		t.Fatal(err)
	}
	h264 := media.Capabilities{Formats: []media.FormatSupport{{Transport: media.TransportMP4, Video: media.VideoSupport{Codec: media.CodecH264}}}}
	encoders := func(_ context.Context, c media.Codec) (ffmpeg.Encoder, bool) {
		return ffmpeg.Encoder{Name: "h264_test_hw", Codec: media.CodecH264, Hardware: true}, c == media.CodecH264
	}
	probe := media.ProbeInfo{VideoCodec: media.CodecH264, VideoHeight: 1080, VideoBitDepth: 8}

	// A served encode (a spliced source cannot be copied) takes the rate.
	served, _, err := decideVideo(t.Context(), Inputs{Caps: h264, Probe: probe, Measured: true, Into: testFormat(t, media.MPEGTS), MaxHeight: 1080, Spliced: true, Rate: r, Encoders: encoders})
	if err != nil {
		t.Fatal(err)
	}
	venc, ok := served.Encode()
	if !ok || venc.Bitrate != "8M" || venc.Maxrate != "8M" || venc.Bufsize != "16M" {
		t.Errorf("served encode = %+v, want 8M under a 16M buffer", venc)
	}

	// So does the buffer's floor encode, which is what keeps its pacer's ceiling real.
	floor, _, err := floorVideoTrack(t.Context(), Inputs{Probe: probe, Measured: true, Into: testFormat(t, media.MPEGTS), Spliced: true, Rate: r, Encoders: encoders})
	if err != nil {
		t.Fatal(err)
	}
	venc, ok = floor.Encode()
	if !ok || venc.Bitrate != "8M" || venc.Maxrate != "8M" || venc.Bufsize != "16M" {
		t.Errorf("floor encode = %+v, want 8M under a 16M buffer", venc)
	}

	// Left unset, both are the old 4M.
	unset, _, err := decideVideo(t.Context(), Inputs{Caps: h264, Probe: probe, Measured: true, Into: testFormat(t, media.MPEGTS), MaxHeight: 1080, Spliced: true, Encoders: encoders})
	if err != nil {
		t.Fatal(err)
	}
	if venc, _ := unset.Encode(); venc.Bitrate != "4M" || venc.Bufsize != "8M" {
		t.Errorf("an unset rate planned %+v, want 4M under an 8M buffer", venc)
	}
}

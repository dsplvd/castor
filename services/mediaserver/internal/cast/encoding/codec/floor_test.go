package codec

import (
	"context"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

func TestMeasuredAbsentTrackDoesNotRequestAnEncoderAtASplice(t *testing.T) {
	for _, tt := range []struct {
		name  string
		probe media.ProbeInfo
	}{
		{name: "audio only", probe: media.ProbeInfo{AudioCodec: media.CodecAAC}},
		{name: "video only", probe: media.ProbeInfo{VideoCodec: media.CodecH264}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			encoderCalls := 0
			in := Inputs{
				Probe: tt.probe, Measured: true, Spliced: true,
				Encoders: func(context.Context, media.Codec) (ffmpeg.Encoder, bool) {
					encoderCalls++
					return ffmpeg.Encoder{}, false
				},
			}
			if tt.probe.VideoCodec == "" {
				video, reasons, err := floorVideoTrack(t.Context(), in)
				if err != nil || video.Name() != "copy" || len(reasons) != 0 || encoderCalls != 0 {
					t.Fatalf("absent video: track=%s reasons=%v encoder calls=%d err=%v", video.Name(), reasons, encoderCalls, err)
				}
			} else {
				audio, reasons := floorAudioTrack(in)
				if audio.Name() != "copy" || len(reasons) != 0 {
					t.Fatalf("absent audio: track=%s reasons=%v", audio.Name(), reasons)
				}
			}
		})
	}
}

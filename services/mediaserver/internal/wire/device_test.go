package wire

import (
	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"testing"
)

func TestCapabilitiesPreserveAtomicTransportAndTracks(t *testing.T) {
	caps := &mediav1.Capabilities{Formats: []*mediav1.FormatSupport{
		{Transport: &mediav1.FormatSupport_Progressive{Progressive: &mediav1.ProgressiveSupport{Container: mediav1.ProgressiveSupport_CONTAINER_MP4}}, Tracks: &mediav1.FormatSupport_Av{Av: &mediav1.AVSupport{Video: &mediav1.VideoSupport{Codec: mediav1.VideoCodec_VIDEO_CODEC_H264}, Audio: &mediav1.AudioSupport{Codec: mediav1.AudioCodec_AUDIO_CODEC_AAC, MaxChannels: 2}}}, Captions: []mediav1.CaptionFormat{mediav1.CaptionFormat_CAPTION_FORMAT_VTT}},
		{Transport: &mediav1.FormatSupport_Hls{Hls: &mediav1.HLSSupport{}}, Tracks: &mediav1.FormatSupport_Audio{Audio: &mediav1.AudioSupport{Codec: mediav1.AudioCodec_AUDIO_CODEC_AC3}}},
		{Transport: &mediav1.FormatSupport_Hls{Hls: &mediav1.HLSSupport{Subtitles: mediav1.HLSSupport_SUBTITLES_WEBVTT}}, Tracks: &mediav1.FormatSupport_Video{Video: &mediav1.VideoSupport{Codec: mediav1.VideoCodec_VIDEO_CODEC_HEVC, BitDepths: []uint32{10}}}},
	}}
	got := FromCapabilities(caps)
	if len(got.Formats) != 3 || got.Formats[0].Transport != media.TransportMP4 || got.Formats[1].Transport != media.TransportHLS || got.Formats[2].Transport != media.TransportHLSWebVTT {
		t.Fatalf("transport variants lost: %+v", got)
	}
	if got.Formats[0].Video.Codec != media.CodecH264 || got.Formats[0].Audio.Codec != media.CodecAAC || got.Formats[0].Audio.MaxChannels != 2 || len(got.Formats[0].Captions) != 1 {
		t.Fatalf("AV envelope lost: %+v", got.Formats[0])
	}
	if got.Formats[1].Video.Codec != "" || got.Formats[1].Audio.Codec != media.CodecAC3 || got.Formats[2].Audio.Codec != "" || got.Formats[2].Video.Codec != media.CodecHEVC {
		t.Fatalf("single-track variants widened: %+v", got)
	}
	if len(FromCapabilities(nil).Formats) != 0 {
		t.Fatal("absent capabilities widened")
	}
}

func TestCapabilitiesRejectUnknownVariantsWithoutWideningProfiles(t *testing.T) {
	for name, f := range map[string]*mediav1.FormatSupport{
		"unknown transport":     {Transport: &mediav1.FormatSupport_Progressive{Progressive: &mediav1.ProgressiveSupport{Container: 999}}, Tracks: &mediav1.FormatSupport_Video{Video: &mediav1.VideoSupport{Codec: mediav1.VideoCodec_VIDEO_CODEC_H264}}},
		"unknown subtitle mode": {Transport: &mediav1.FormatSupport_Hls{Hls: &mediav1.HLSSupport{Subtitles: 999}}, Tracks: &mediav1.FormatSupport_Video{Video: &mediav1.VideoSupport{Codec: mediav1.VideoCodec_VIDEO_CODEC_H264}}},
		"unknown codec":         {Transport: &mediav1.FormatSupport_Hls{Hls: &mediav1.HLSSupport{}}, Tracks: &mediav1.FormatSupport_Video{Video: &mediav1.VideoSupport{Codec: 999}}},
		"unknown profile":       {Transport: &mediav1.FormatSupport_Hls{Hls: &mediav1.HLSSupport{}}, Tracks: &mediav1.FormatSupport_Video{Video: &mediav1.VideoSupport{Codec: mediav1.VideoCodec_VIDEO_CODEC_HEVC, Profiles: []mediav1.Profile{999}}}},
	} {
		t.Run(name, func(t *testing.T) {
			if len(FromCapabilities(&mediav1.Capabilities{Formats: []*mediav1.FormatSupport{f}}).Formats) != 0 {
				t.Fatal("invalid envelope widened")
			}
		})
	}
}

func TestIncompleteAVEnvelopeIsNotWidenedToOneTrack(t *testing.T) {
	f := &mediav1.FormatSupport{
		Transport: &mediav1.FormatSupport_Hls{Hls: &mediav1.HLSSupport{}},
		Tracks:    &mediav1.FormatSupport_Av{Av: &mediav1.AVSupport{Video: &mediav1.VideoSupport{Codec: mediav1.VideoCodec_VIDEO_CODEC_H264}}},
	}
	if got := FromCapabilities(&mediav1.Capabilities{Formats: []*mediav1.FormatSupport{f}}); len(got.Formats) != 0 {
		t.Fatalf("incomplete AV became usable: %+v", got)
	}
}

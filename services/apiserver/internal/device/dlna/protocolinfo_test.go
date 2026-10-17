package dlna

import (
	"slices"
	"testing"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
)

func TestParseSinkProtocolInfo(t *testing.T) {
	avcSink := "http-get:*:audio/mpeg:*," +
		"http-get:*:video/mp2t:DLNA.ORG_PN=AVC_TS_HD_50_AC3_ISO," +
		"http-get:*:video/mp4:DLNA.ORG_PN=AVC_MP4_MP_HD_AAC"

	caps, _ := parseSinkProtocolInfo(avcSink)
	withHEVC, _ := parseSinkProtocolInfo(avcSink + ",http-get:*:video/mp2t:DLNA.ORG_PN=HEVC_TS_MAIN_HD_AAC")
	if !slices.ContainsFunc(withHEVC.Formats, func(f *mediav1.FormatSupport) bool {
		return f.GetAv().GetVideo().GetCodec() == mediav1.VideoCodec_VIDEO_CODEC_HEVC
	}) {
		t.Fatal("HEVC tuple missing")
	}
	if len(caps.Formats) != 2 || caps.Formats[0].GetAv().GetAudio().GetCodec() != mediav1.AudioCodec_AUDIO_CODEC_AC3 || caps.Formats[1].GetProgressive().GetContainer() != mediav1.ProgressiveSupport_CONTAINER_MP4 || caps.Formats[1].GetAv().GetAudio().GetCodec() != mediav1.AudioCodec_AUDIO_CODEC_AAC {
		t.Fatalf("tuples=%v", caps.Formats)
	}
	// No video codec makes negotiateCaps refuse the device.
	if got, _ := parseSinkProtocolInfo("garbage,http-get:*:audio/mpeg:*"); len(got.Formats) != 0 {
		t.Errorf("unusable sink should yield no video, got %v", got.Formats)
	}
}

func TestRepeatedSinkProfilesDoNotDuplicateCapabilities(t *testing.T) {
	const entry = "http-get:*:video/mp2t:DLNA.ORG_PN=AVC_TS_HD_50_AC3_ISO"
	caps, sinks := parseSinkProtocolInfo(entry + "," + entry + "," + entry)
	if len(caps.Formats) != 1 {
		t.Fatalf("repeated sink entry produced %d formats", len(caps.Formats))
	}
	if sinks[mediav1.Container_CONTAINER_MPEGTS].features != "DLNA.ORG_PN=AVC_TS_HD_50_AC3_ISO" {
		t.Fatalf("duplicate entry changed sink metadata: %+v", sinks)
	}
}

func TestAContainerIsSpokenForByTheEntryNamingACodec(t *testing.T) {
	caps, sinks := parseSinkProtocolInfo("http-get:*:video/mpeg:*,http-get:*:video/mp2t:DLNA.ORG_PN=MPEG_TS_SD_EU,http-get:*:video/mp2t:DLNA.ORG_PN=AVC_TS_HD_50_AC3_ISO")
	if got := sinks[mediav1.Container_CONTAINER_MPEGTS].features; got != "DLNA.ORG_PN=AVC_TS_HD_50_AC3_ISO" {
		t.Errorf("MPEG-TS entry = %q, want the one naming a codec", got)
	}
	if len(caps.Formats) != 1 || caps.Formats[0].GetProgressive().GetContainer() != mediav1.ProgressiveSupport_CONTAINER_MPEGTS {
		t.Fatalf("expected MPEG-TS envelope: %v", caps)
	}

}

func TestSeparateAudioDoesNotWidenAVProfile(t *testing.T) {
	caps, _ := parseSinkProtocolInfo("http-get:*:video/mp2t:DLNA.ORG_PN=AVC_TS_HD_AC3_ISO,http-get:*:audio/mp4:DLNA.ORG_PN=AAC_ISO")
	if len(caps.Formats) != 1 || caps.Formats[0].GetAv().GetAudio().GetCodec() != mediav1.AudioCodec_AUDIO_CODEC_AC3 {
		t.Fatalf("widened capability: %v", caps)
	}
	if caps.Deinterlaces || len(caps.Formats[0].Captions) > 0 {
		t.Fatal("unadvertised receiver features assumed")
	}
}
func TestMultipleProfilesDoNotAdvertiseWrongMetadata(t *testing.T) {
	caps, sinks := parseSinkProtocolInfo("http-get:*:video/mp2t:DLNA.ORG_PN=AVC_TS_HD_AC3_ISO,http-get:*:video/mp2t:DLNA.ORG_PN=AVC_TS_HD_AAC_ISO")
	if len(caps.Formats) != 2 || sinks[mediav1.Container_CONTAINER_MPEGTS].features != "*" {
		t.Fatalf("caps=%v sinks=%v", caps, sinks)
	}
}

func TestStaticWebVTTDoesNotImplyProgressiveSubtitles(t *testing.T) {
	caps, _ := parseSinkProtocolInfo("http-get:*:application/x-mpegURL:*,http-get:*:text/vtt:*,http-get:*:video/mp4:DLNA.ORG_PN=AVC_MP4_MP_HD_AAC")
	if slices.ContainsFunc(caps.Formats, func(f *mediav1.FormatSupport) bool { return f.GetHls() != nil }) {
		t.Fatal("DLNA protocolInfo does not establish HLS subtitle rendition support")
	}
}

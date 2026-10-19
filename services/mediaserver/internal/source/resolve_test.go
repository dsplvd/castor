package source

import (
	"net/url"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

func TestNarrowedRenditionRetainsObservedWrappedSegments(t *testing.T) {
	stream := &Stream{
		URL:         &url.URL{Scheme: "https", Host: "origin.example", Path: "/master.m3u8"},
		ContentType: media.HLS,
		Probe:       &media.ProbeInfo{SourceUnwrapped: true},
	}
	rung := &url.URL{Scheme: "https", Host: "origin.example", Path: "/video.m3u8"}
	program := media.Program{
		Inputs:     []media.Input{{ID: media.PrimaryInputID, URL: rung, ContentType: media.HLS}},
		Tracks:     []media.TrackRef{{Input: media.PrimaryInputID, Kind: media.TrackVideo}},
		ClockInput: media.PrimaryInputID,
		EndPolicy:  media.EndAtLongest,
	}
	narrowed, err := Narrowed(program, Rendition{Declared: &media.ProbeInfo{VideoCodec: media.CodecH264}}, stream)
	if err != nil {
		t.Fatal(err)
	}
	measured, ok := narrowed.Measurement()
	if !ok || !measured.SourceUnwrapped || measured.VideoCodec != media.CodecH264 {
		t.Fatalf("narrowed measurement = %+v, measured=%t", measured, ok)
	}
}

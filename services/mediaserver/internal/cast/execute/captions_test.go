package execute

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/captions"
	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
	"github.com/stupside/castor/services/mediaserver/internal/source/timeline"
)

type textPages map[string]string

func (p textPages) Fetch(_ context.Context, u *url.URL, _ http.Header) (string, *url.URL, int, error) {
	return p[u.Path], u, http.StatusOK, nil
}
func (textPages) Replay(_ *url.URL, h http.Header) http.Header { return h }
func (textPages) Read(context.Context, *url.URL, http.Header, timeline.Range) (io.ReadCloser, error) {
	return nil, io.EOF
}

func captionedPipeline(t *testing.T, caps media.Capabilities, want captions.Want) *pipeline {
	t.Helper()
	published := []source.Caption{{URL: &url.URL{Scheme: "http", Host: "origin.test", Path: "/subs/en.vtt"}, Language: "en", Name: "English"}}
	client := textPages{"/subs/en.vtt": "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nhello\n"}
	return &pipeline{
		ctx:      t.Context(),
		releases: &releases{},
		cast:     Cast{Device: &fakeDevice{caps: caps}, Listeners: loopback{}, Captions: &Captions{Want: want, Client: client}},
		attempt: recovery.Attempt{
			Program:   media.Program{Inputs: []media.Input{{ID: media.PrimaryInputID, URL: &url.URL{Scheme: "http", Host: "origin.test", Path: "/v.m3u8"}}}},
			Rendition: source.Rendition{Captions: published},
		},
	}
}

func TestTheSourcesCaptionIsServedToADeviceThatShowsThem(t *testing.T) {
	s := captionedPipeline(t, media.Capabilities{Captions: []media.CaptionFormat{media.CaptionSRT}}, captions.Want{Language: "en"})
	t.Cleanup(func() { _ = s.releases.release() })
	shown := s.shownCaptions()
	if len(shown) != 1 || shown[0].Format != media.CaptionSRT || !shown[0].Default {
		t.Fatalf("shown = %+v, want one SubRip caption", shown)
	}
	resp, err := http.Get(shown[0].URL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "00:00:01,000 --> 00:00:02,000\nhello") {
		t.Errorf("served %q", body)
	}
}

func TestNoCaptionIsShownWhenTheDeviceTakesNoneOrTheLanguageIsAbsent(t *testing.T) {
	for name, s := range map[string]*pipeline{
		"a device with no caption format": captionedPipeline(t, media.Capabilities{}, captions.Want{Language: "en"}),
		"a language the source lacks":     captionedPipeline(t, media.Capabilities{Captions: []media.CaptionFormat{media.CaptionVTT}}, captions.Want{Language: "fr"}),
	} {
		if shown := s.shownCaptions(); len(shown) != 0 {
			t.Errorf("%s: shown = %+v, want none", name, shown)
		}
	}
}

func TestTheSourcesCaptionsReplaceTheTranscription(t *testing.T) {
	s := captionedPipeline(t, media.Capabilities{Captions: []media.CaptionFormat{media.CaptionVTT}}, captions.Want{Language: "en"})
	t.Cleanup(func() { _ = s.releases.release() })
	s.cast.Subtitles = func(context.Context, string) Burn {
		t.Error("transcription started beside the source's own captions")
		return nil
	}
	s.shown = s.shownCaptions()
	if burn := s.transcription(t.Context(), t.TempDir(), facts{}, media.Program{}); burn != nil {
		t.Errorf("burn = %v, want none", burn)
	}
}

package captions

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/source"
	"github.com/stupside/castor/services/mediaserver/internal/source/timeline"
)

func TestPickFollowsTheLanguageAskedOrTheSourcesOwnMarks(t *testing.T) {
	en := source.Caption{Language: "en-US", Name: "English"}
	fr := source.Caption{Language: "fr", Forced: true}
	de := source.Caption{Language: "de", Default: true}
	all := []source.Caption{en, fr, de}
	for _, tc := range []struct {
		name string
		want Want
		got  source.Caption
		ok   bool
	}{
		{"a code matches its primary subtag", Want{Language: "en"}, en, true},
		{"a language the source lacks gives none", Want{Language: "es"}, source.Caption{}, false},
		{"auto prefers the default", Want{Auto: true}, de, true},
		{"auto takes the forced without a default", Want{Auto: true}, fr, true},
	} {
		list := all
		if tc.name == "auto takes the forced without a default" {
			list = []source.Caption{en, fr}
		}
		got, ok := Pick(list, tc.want)
		if ok != tc.ok || got.Language != tc.got.Language {
			t.Errorf("%s: Pick = %+v, %v", tc.name, got, ok)
		}
	}
}

func TestMergeMovesEachSegmentOntoTheStreamsClock(t *testing.T) {
	first := "WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:900000,LOCAL:00:00:00.000\n\n00:00:01.000 --> 00:00:02.000\nhello\n"
	second := "WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:1440000,LOCAL:00:00:00.000\n\n00:00:01.000 --> 00:00:03.500 align:start\nworld\n"
	got := merge([]string{first, second}, 10*time.Second)
	// 1440000/90000 = 16s against a first map of 10s: the second segment sits 6s in.
	for _, want := range []string{"00:00:01.000 --> 00:00:02.000\nhello", "00:00:07.000 --> 00:00:09.500 align:start\nworld"} {
		if !strings.Contains(got, want) {
			t.Errorf("merged = %q, want it to contain %q", got, want)
		}
	}
}

func TestSegmentsWithoutAMapKeepTheirTimes(t *testing.T) {
	got := merge([]string{"WEBVTT\n\n00:01.500 --> 00:02.000\nhi\n"}, 0)
	if !strings.Contains(got, "00:00:01.500 --> 00:00:02.000\nhi") {
		t.Errorf("merged = %q", got)
	}
}

func TestSRTRewritesTheSameCues(t *testing.T) {
	got := SRT("WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nhello\nthere\n")
	if got != "1\n00:00:01,000 --> 00:00:02,000\nhello\nthere\n\n" {
		t.Errorf("SRT = %q", got)
	}
}

type pages map[string]string

func (p pages) Fetch(_ context.Context, u *url.URL, _ http.Header) (string, *url.URL, int, error) {
	return p[u.Path], u, 200, nil
}
func (pages) Replay(_ *url.URL, h http.Header) http.Header { return h }
func (pages) Read(context.Context, *url.URL, http.Header, timeline.Range) (io.ReadCloser, error) {
	return nil, nil
}

func TestFetchJoinsAPlaylistsSegments(t *testing.T) {
	client := pages{
		"/subs/en.m3u8":  "#EXTM3U\n#EXTINF:6,\nseg0.vtt\n#EXTINF:6,\nseg1.vtt\n#EXT-X-ENDLIST\n",
		"/subs/seg0.vtt": "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\none\n",
		"/subs/seg1.vtt": "WEBVTT\n\n00:00:07.000 --> 00:00:08.000\ntwo\n",
	}
	got, err := FetchAt(t.Context(), client, source.Caption{URL: &url.URL{Scheme: "http", Host: "h", Path: "/subs/en.m3u8"}}, nil, nil)
	if err != nil || !strings.Contains(got, "one") || !strings.Contains(got, "two") {
		t.Errorf("Fetch = %q, %v", got, err)
	}
}

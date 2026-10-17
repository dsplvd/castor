package captions

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/source"
)

func TestInvalidCaptionsDoNotDisableFallback(t *testing.T) {
	u := &url.URL{Scheme: "http", Host: "origin", Path: "/captions"}
	for name, body := range map[string]string{
		"empty":          "WEBVTT\n\n",
		"malformed":      "WEBVTT\n\nbad --> times\nhello\n",
		"reversed":       "WEBVTT\n\n00:00:02.000 --> 00:00:01.000\nhello\n",
		"empty text":     "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\n\n",
		"not a playlist": "<html>error</html>",
		"live":           "#EXTM3U\n#EXTINF:6,\nsegment.vtt\n",
		"unknown clock":  "WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:900000,LOCAL:00:00:10.000\n\n00:00:11.000 --> 00:00:12.000\nhello\n",
		"bad map":        "WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:-1,LOCAL:00:00:00.000\n\n00:00:01.000 --> 00:00:02.000\nhello\n",
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := FetchAt(t.Context(), pages{"/captions": body}, source.Caption{URL: u}, nil, nil); err == nil {
				t.Fatalf("unusable captions accepted: %q", got)
			}
		})
	}
}

func TestCaptionsUseTheMediaOriginWithNonzeroLocalMap(t *testing.T) {
	u := &url.URL{Scheme: "http", Host: "origin", Path: "/captions"}
	client := pages{"/captions": "WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:900000,LOCAL:00:00:10.000\n\n00:00:11.000 --> 00:00:12.000\nhello\n"}
	origin := 10 * time.Second
	got, err := FetchAt(t.Context(), client, source.Caption{URL: u}, nil, &origin)
	if err != nil || !strings.Contains(got, "00:00:01.000 --> 00:00:02.000") {
		t.Fatalf("captions=%q: %v", got, err)
	}
}

func TestCaptionFetchHasAggregateBoundsAndCancellation(t *testing.T) {
	u := &url.URL{Scheme: "http", Host: "origin", Path: "/captions"}
	for name, client := range map[string]pages{
		"bytes":    {"/captions": "WEBVTT\n" + strings.Repeat("x", maxCaptionBytes)},
		"segments": {"/captions": "#EXTM3U\n" + strings.Repeat("segment.vtt\n", maxCaptionSegments+1) + "#EXT-X-ENDLIST\n", "/segment.vtt": "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nhello\n"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := FetchAt(t.Context(), client, source.Caption{URL: u}, nil, nil); err == nil {
				t.Fatal("budget exceeded without error")
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := FetchAt(ctx, pages{}, source.Caption{URL: u}, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
}

func TestCaptionTimesRejectNonFiniteNegativeAndInvalidFields(t *testing.T) {
	for _, value := range []string{"00:NaN", "00:Inf", "00:-1", "00:60", "-1:00", "999999999999999:00"} {
		if _, ok := parseTime(value); ok {
			t.Errorf("accepted %q", value)
		}
	}
}

func TestCaptionTimestampRolloverUsesTheSharedOrigin(t *testing.T) {
	const ticks = int64(1 << 33)
	origin := time.Duration(ticks-2*90000) * time.Second / 90000
	first := fmt.Sprintf("WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:%d,LOCAL:00:00:00.000\n\n00:00:00.000 --> 00:00:00.500\nbefore\n", ticks-90000)
	second := "WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:90000,LOCAL:00:00:00.000\n\n00:00:00.000 --> 00:00:00.500\nafter\n"
	got := merge([]string{first, second}, origin)
	if !strings.Contains(got, "00:00:01.000 --> 00:00:01.500") || !strings.Contains(got, "00:00:03.000 --> 00:00:03.500") {
		t.Fatalf("rollover captions=%q", got)
	}
}

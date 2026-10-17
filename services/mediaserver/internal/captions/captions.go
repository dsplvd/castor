// Package captions turns the text renditions a source published into one caption file a device can show.
package captions

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/source"
)

// Matches returns published captions in preference order, including alternative tracks
// in the same language so a broken track does not hide another usable one.
func Matches(available []source.Caption, languages []string) []source.Caption {
	var matches []source.Caption
	seen := make([]bool, len(available))
	for _, language := range languages {
		for i, c := range available {
			if !seen[i] && source.SameLanguage(c.Language, language) {
				matches = append(matches, c)
				seen[i] = true
			}
		}
	}
	return matches
}

const (
	maxCaptionSegments = 2048
	maxCaptionBytes    = 8 << 20
	captionDeadline    = 30 * time.Second
)

// FetchAt normalizes mapped captions against the first timestamp of the video.
// A nil origin means no shared clock has been established.
func FetchAt(ctx context.Context, client source.Client, c source.Caption, headers http.Header, origin *time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, captionDeadline)
	defer cancel()
	if c.URL == nil {
		return "", fmt.Errorf("caption URL is missing")
	}
	bytes := 0
	fetch := func(u *url.URL) (string, *url.URL, error) {
		if err := ctx.Err(); err != nil {
			return "", u, err
		}
		body, from, _, err := client.Fetch(ctx, u, headers)
		if err != nil {
			return "", from, err
		}
		if err := ctx.Err(); err != nil {
			return "", from, err
		}
		if len(body) > maxCaptionBytes-bytes {
			return "", from, fmt.Errorf("captions exceed %d bytes", maxCaptionBytes)
		}
		bytes += len(body)
		if from == nil {
			from = u
		}
		return body, from, nil
	}
	body, from, err := fetch(c.URL)
	if err != nil {
		return "", fmt.Errorf("reading captions: %w", err)
	}
	var pieces []string
	if documentHeader(body, "WEBVTT") {
		pieces = []string{body}
	} else {
		if !documentHeader(body, "#EXTM3U") {
			return "", fmt.Errorf("caption document is neither WebVTT nor HLS")
		}
		// An immutable file cannot represent a rolling playlist. Refuse it explicitly
		// so it cannot suppress transcription after its initial window.
		complete := false
		for line := range strings.Lines(body) {
			if strings.TrimSpace(line) == "#EXT-X-ENDLIST" {
				complete = true
			}
		}
		if !complete {
			return "", fmt.Errorf("live source captions require timeline-following delivery")
		}
		for line := range strings.Lines(body) {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "#EXT-X-DISCONTINUITY") {
				return "", fmt.Errorf("caption discontinuity requires a new shared media clock")
			}
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if len(pieces) >= maxCaptionSegments {
				return "", fmt.Errorf("captions exceed %d segments", maxCaptionSegments)
			}
			segment, err := from.Parse(line)
			if err != nil {
				return "", fmt.Errorf("caption segment: %w", err)
			}
			piece, _, err := fetch(segment)
			if err != nil {
				return "", fmt.Errorf("reading caption segment: %w", err)
			}
			pieces = append(pieces, piece)
		}
	}
	for _, piece := range pieces {
		if !documentHeader(piece, "WEBVTT") {
			return "", fmt.Errorf("invalid WebVTT segment")
		}
		_, _, mapped := parse(piece)
		if headerHasMap(piece) && !mapped {
			return "", fmt.Errorf("invalid caption timestamp map")
		}
		if mapped && origin == nil {
			return "", fmt.Errorf("mapped captions require a measured media origin")
		}
	}
	start := time.Duration(0)
	if origin != nil {
		start = *origin
	}
	merged := merge(pieces, start)
	cues, _, _ := parse(merged)
	if len(cues) == 0 {
		return "", fmt.Errorf("captions contain no usable cues")
	}
	return merged, nil
}

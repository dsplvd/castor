// Package captions turns the text renditions a source published into one caption file a device can show.
package captions

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/source"
)

// bom is the byte order mark some servers put before WEBVTT.
const bom = "\uFEFF"

// Want is the language a cast asked its captions in: a code, or Auto for whichever the source marks first.
type Want struct {
	Language string
	Auto     bool
}

// Pick is the caption of the source that Want selects, none when it has not the language asked.
// Auto takes the source's default, then its forced, then its first.
func Pick(available []source.Caption, want Want) (source.Caption, bool) {
	if want.Auto {
		for _, c := range available {
			if c.Default {
				return c, true
			}
		}
		for _, c := range available {
			if c.Forced {
				return c, true
			}
		}
		if len(available) == 0 {
			return source.Caption{}, false
		}
		return available[0], true
	}
	for _, c := range available {
		if source.SameLanguage(c.Language, want.Language) {
			return c, true
		}
	}
	return source.Caption{}, false
}

// Fetch reads the WebVTT playlist at c and merges its segments into one WebVTT document on the stream's own timeline.
func Fetch(ctx context.Context, client source.Client, c source.Caption, headers http.Header) (string, error) {
	body, from, _, err := client.Fetch(ctx, c.URL, headers)
	if err != nil {
		return "", fmt.Errorf("reading the caption playlist: %w", err)
	}
	if strings.HasPrefix(strings.TrimPrefix(body, bom), "WEBVTT") {
		return merge([]string{body}), nil
	}
	var pieces []string
	for line := range strings.Lines(body) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		segment, err := from.Parse(line)
		if err != nil {
			return "", fmt.Errorf("caption segment %q: %w", line, err)
		}
		piece, _, _, err := client.Fetch(ctx, segment, headers)
		if err != nil {
			return "", fmt.Errorf("reading caption segment %s: %w", segment.Path, err)
		}
		pieces = append(pieces, piece)
	}
	if len(pieces) == 0 {
		return "", fmt.Errorf("the caption playlist %s lists no segments", c.URL.Path)
	}
	return merge(pieces), nil
}

// cue is one caption shown from start to end; settings and text are carried as written.
type cue struct {
	start, end time.Duration
	settings   string
	text       string
}

// merge joins WebVTT segments into one document. A segment's X-TIMESTAMP-MAP places its cue times on the stream's clock, and
// times move so the first mapped segment starts the document at zero, the clock a device plays the stream on.
func merge(pieces []string) string {
	var cues []cue
	var base time.Duration
	haveBase := false
	for _, piece := range pieces {
		parsed, offset, mapped := parse(piece)
		if mapped && !haveBase {
			base, haveBase = offset, true
		}
		if mapped {
			offset -= base
		} else {
			offset = 0
		}
		for _, c := range parsed {
			c.start, c.end = max(c.start+offset, 0), max(c.end+offset, 0)
			cues = append(cues, c)
		}
	}
	var out strings.Builder
	out.WriteString("WEBVTT\n\n")
	for _, c := range cues {
		fmt.Fprintf(&out, "%s --> %s%s\n%s\n\n", clock(c.start, '.'), clock(c.end, '.'), c.settings, c.text)
	}
	return out.String()
}

// parse reads one segment's cues and the offset its X-TIMESTAMP-MAP adds (mapped is false without one).
// A block is the lines between blank ones; the header's lines are WEBVTT and the map, and a block without a timing line is not a cue.
func parse(piece string) (cues []cue, offset time.Duration, mapped bool) {
	piece = strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(strings.TrimPrefix(piece, bom))
	for block := range strings.SplitSeq(piece, "\n\n") {
		lines := strings.Split(strings.Trim(block, "\n"), "\n")
		for _, line := range lines {
			if v, ok := strings.CutPrefix(line, "X-TIMESTAMP-MAP="); ok {
				offset, mapped = timestampMap(v)
			}
		}
		timing := slices.IndexFunc(lines, func(line string) bool { return strings.Contains(line, "-->") })
		if timing < 0 {
			continue
		}
		start, rest, _ := strings.Cut(lines[timing], " --> ")
		end, settings, _ := strings.Cut(strings.TrimSpace(rest), " ")
		from, okFrom := parseTime(strings.TrimSpace(start))
		to, okTo := parseTime(end)
		if !okFrom || !okTo {
			continue
		}
		if settings != "" {
			settings = " " + settings
		}
		cues = append(cues, cue{start: from, end: to, settings: settings, text: strings.Join(lines[timing+1:], "\n")})
	}
	return cues, offset, mapped
}

// timestampMap is MPEGTS:90000,LOCAL:00:00:00.000 as the shift from cue time to stream time.
func timestampMap(v string) (time.Duration, bool) {
	var mpegts int64
	var local time.Duration
	var haveTS, haveLocal bool
	for part := range strings.SplitSeq(v, ",") {
		key, val, _ := strings.Cut(strings.TrimSpace(part), ":")
		switch key {
		case "MPEGTS":
			n, err := strconv.ParseInt(val, 10, 64)
			mpegts, haveTS = n, err == nil
		case "LOCAL":
			local, haveLocal = parseTime(val)
		}
	}
	if !haveTS || !haveLocal {
		return 0, false
	}
	return time.Duration(mpegts)*time.Second/90000 - local, true
}

// parseTime reads hh:mm:ss.mmm or mm:ss.mmm.
func parseTime(s string) (time.Duration, bool) {
	s = strings.Replace(s, ",", ".", 1)
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	var total float64
	for _, p := range parts {
		n, err := strconv.ParseFloat(p, 64)
		if err != nil {
			return 0, false
		}
		total = total*60 + n
	}
	return time.Duration(total * float64(time.Second)), true
}

func clock(d time.Duration, point byte) string {
	ms := d.Milliseconds()
	return fmt.Sprintf("%02d:%02d:%02d%c%03d", ms/3600000, ms/60000%60, ms/1000%60, point, ms%1000)
}

// SRT is a WebVTT document written as SubRip, for the devices that read only that.
func SRT(vtt string) string {
	cues, _, _ := parse(vtt)
	var out strings.Builder
	for i, c := range cues {
		fmt.Fprintf(&out, "%d\n%s --> %s\n%s\n\n", i+1, clock(c.start, ','), clock(c.end, ','), c.text)
	}
	return out.String()
}

package captions

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

const bom = "\uFEFF"

var captionNewlines = strings.NewReplacer("\r\n", "\n", "\r", "\n")

// cue is one caption shown from start to end; settings and text are carried as written.
type cue struct {
	start, end time.Duration
	settings   string
	text       string
}

// parse reads one segment's cues and the offset its X-TIMESTAMP-MAP adds (mapped is false without one).
// A block is the lines between blank ones; the header's lines are WEBVTT and the map, and a block without a timing line is not a cue.
func parse(piece string) (cues []cue, offset time.Duration, mapped bool) {
	piece = captionNewlines.Replace(strings.TrimPrefix(piece, bom))
	for block := range strings.SplitSeq(piece, "\n\n") {
		lines := strings.Split(strings.Trim(block, "\n"), "\n")
		first := strings.TrimSpace(lines[0])
		if first == "NOTE" || strings.HasPrefix(first, "NOTE ") || strings.HasPrefix(first, "NOTE\t") || first == "STYLE" || first == "REGION" {
			continue
		}
		if documentHeader(block, "WEBVTT") {
			for _, line := range lines {
				if v, ok := strings.CutPrefix(line, "X-TIMESTAMP-MAP="); ok {
					offset, mapped = timestampMap(v)
				}
			}
			continue
		}
		timing := slices.IndexFunc(lines, func(line string) bool { return strings.Contains(line, "-->") })
		if timing < 0 {
			continue
		}
		fields := strings.Fields(lines[timing])
		if len(fields) < 3 || fields[1] != "-->" {
			continue
		}
		from, okFrom := parseTime(fields[0])
		to, okTo := parseTime(fields[2])
		if !okFrom || !okTo || to <= from || strings.TrimSpace(strings.Join(lines[timing+1:], "\n")) == "" {
			continue
		}
		settings := ""
		if len(fields) > 3 {
			settings = " " + strings.Join(fields[3:], " ")
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
			mpegts, haveTS = n, err == nil && n >= 0 && n < 1<<33
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
	for i, p := range parts {
		if i < len(parts)-1 {
			if _, err := strconv.ParseUint(p, 10, 64); err != nil {
				return 0, false
			}
		}
		n, err := strconv.ParseFloat(p, 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || (i > 0 && n >= 60) {
			return 0, false
		}
		total = total*60 + n
	}
	if total >= float64(math.MaxInt64)/float64(time.Second) {
		return 0, false
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

// documentHeader recognizes an entire signature line, not a prefix of invalid
// text. WebVTT permits a description separated by a space or tab.
func documentHeader(body, signature string) bool {
	body = captionNewlines.Replace(body)
	line, _, _ := strings.Cut(strings.TrimPrefix(body, bom), "\n")
	line = strings.TrimSuffix(line, "\r")
	return line == signature || signature == "WEBVTT" && (strings.HasPrefix(line, "WEBVTT ") || strings.HasPrefix(line, "WEBVTT\t"))
}

func headerHasMap(piece string) bool {
	piece = captionNewlines.Replace(piece)
	header, _, _ := strings.Cut(piece, "\n\n")
	for line := range strings.Lines(header) {
		if strings.HasPrefix(line, "X-TIMESTAMP-MAP") {
			return true
		}
	}
	return false
}

package codec

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// Rate is the video bitrate a server encodes at. H.264 is encoded at the rate itself, under a VBV ceiling equal to it
// and a buffer twice it; HEVC carries the same picture in half the bits. The zero Rate is DefaultRate.
type Rate struct{ bits int64 }

const (
	// MinRate and MaxRate bound a setting to what a typo cannot cross: below the first a picture falls apart, above
	// the second no device castor serves can follow, and the buffer (twice the rate) would be absurd.
	MinRate = 500_000
	MaxRate = 40_000_000

	defaultRate = 4_000_000
)

// DefaultRate is what a server encodes H.264 at unless told otherwise.
var DefaultRate = Rate{bits: defaultRate}

var rateSyntax = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)([kKmM]?)$`)

// ParseRate reads a bitrate the way ffmpeg spells it: a number with an optional k or M suffix (8M, 6000k, 1.5M, 8000000).
func ParseRate(s string) (Rate, error) {
	m := rateSyntax.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return Rate{}, fmt.Errorf("video bitrate %q is not a number with an optional k or M suffix, such as 8M or 6000k", s)
	}
	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return Rate{}, fmt.Errorf("video bitrate %q: %w", s, err)
	}
	switch strings.ToLower(m[2]) {
	case "k":
		n *= 1_000
	case "m":
		n *= 1_000_000
	}
	bits := int64(n + 0.5)
	if bits < MinRate || bits > MaxRate {
		return Rate{}, fmt.Errorf("video bitrate %q is out of range: it must be between %s and %s", s, formatBits(MinRate), formatBits(MaxRate))
	}
	return Rate{bits: bits}, nil
}

// UnmarshalText lets a bitrate be a setting: config.yaml, or an environment variable.
func (r *Rate) UnmarshalText(text []byte) error {
	if len(text) == 0 {
		*r = Rate{}
		return nil
	}
	parsed, err := ParseRate(string(text))
	if err != nil {
		return err
	}
	*r = parsed
	return nil
}

// String is the rate in ffmpeg's spelling.
func (r Rate) String() string { return formatBits(r.orDefault().bits) }

func (r Rate) orDefault() Rate {
	if r.bits == 0 {
		return DefaultRate
	}
	return r
}

// h264 is the target every device decodes: the rate, the same ceiling, and a buffer of twice it.
func (r Rate) h264() videoTarget {
	bits := r.orDefault().bits
	return videoTarget{codec: media.CodecH264, bitrate: formatBits(bits), maxrate: formatBits(bits), bufsize: formatBits(2 * bits)}
}

// hevc is the same picture in half the bits.
func (r Rate) hevc() videoTarget {
	bits := r.orDefault().bits / 2
	return videoTarget{codec: media.CodecHEVC, bitrate: formatBits(bits), maxrate: formatBits(bits), bufsize: formatBits(2 * bits)}
}

// targets is every codec castor encodes video to at this rate, in preference order.
func (r Rate) targets() []videoTarget { return []videoTarget{r.hevc(), r.h264()} }

// formatBits spells a rate for ffmpeg in the shortest exact form: 8M, 6500k, 1234567.
func formatBits(bits int64) string {
	switch {
	case bits%1_000_000 == 0:
		return strconv.FormatInt(bits/1_000_000, 10) + "M"
	case bits%1_000 == 0:
		return strconv.FormatInt(bits/1_000, 10) + "k"
	}
	return strconv.FormatInt(bits, 10)
}

package media

import (
	"net/url"
	"slices"
)

// Capabilities contains atomic playback envelopes in preference order.
type Capabilities struct {
	Formats       []FormatSupport
	Provenance    string
	SelfFetch     bool
	Deinterlaces  bool
	ServedHeaders map[string]string
}

// Transport is a complete delivery variant, including its rendition support.
type Transport uint8

const (
	TransportMP4 Transport = iota + 1
	TransportMPEGTS
	TransportMKV
	TransportWebM
	TransportHLS
	TransportHLSWebVTT
)

func (t Transport) ContentType() string {
	switch t {
	case TransportMP4:
		return MP4
	case TransportMPEGTS:
		return MPEGTS
	case TransportMKV:
		return MKV
	case TransportWebM:
		return WebM
	case TransportHLS, TransportHLSWebVTT:
		return HLS
	}
	return ""
}

// CaptionFormat is how a caption track is written.
type CaptionFormat string

const (
	CaptionVTT CaptionFormat = "vtt"
	CaptionSRT CaptionFormat = "srt"
)

// Caption is a text track a device shows over the stream, fetched at URL like the stream is.
type Caption struct {
	URL      *url.URL
	Format   CaptionFormat
	Language string
	Name     string
	Default  bool
}

// VideoSupport is one video envelope a device decodes natively (codec, profile, bit depth, level).
type VideoSupport struct {
	Codec     Codec
	Profiles  []Profile // nil or empty = any profile
	BitDepths []int     // nil or empty = {8}
	// MaxLevel is the highest level decoded, in ffprobe's units (H.264 x10); 0 = no advertised ceiling.
	MaxLevel int
}

// AudioSupport is one audio codec a device decodes natively, up to MaxChannels channels.
type AudioSupport struct {
	Codec Codec
	// MaxChannels is the highest channel count the device decodes (0 = no advertised ceiling).
	MaxChannels int
}

// AcceptsContainer reports whether the device plays contentType directly (the pass-through decision).
func (c Capabilities) AcceptsContainer(contentType string) bool {
	return slices.ContainsFunc(c.Formats, func(f FormatSupport) bool { return f.Transport.ContentType() == contentType })
}

// PlaybackSampleRate is the highest audio sample rate any device castor serves plays.
const PlaybackSampleRate = 48000

func (c Capabilities) CanCopyVideo(v ProbeInfo) bool {
	if v.VideoInterlaced && !c.Deinterlaces {
		return false
	}
	return slices.ContainsFunc(c.Formats, func(f FormatSupport) bool { return f.Video.accepts(v) })
}

func (c Capabilities) CanCopyAudio(p ProbeInfo) bool {
	if p.AudioSampleRate > PlaybackSampleRate {
		return false
	}
	return slices.ContainsFunc(c.Formats, func(f FormatSupport) bool { return f.Audio.accepts(p) })
}

func (s AudioSupport) accepts(p ProbeInfo) bool {
	if p.AudioCodec != s.Codec {
		return false
	}
	// Unknown source channel count (0) is trusted: don't force-transcode a matching codec with no layout.
	if s.MaxChannels > 0 && p.AudioChannels > s.MaxChannels {
		return false
	}
	return true
}

func (s VideoSupport) accepts(v ProbeInfo) bool {
	if v.VideoCodec != s.Codec {
		return false
	}
	if len(s.Profiles) > 0 && !slices.Contains(s.Profiles, v.VideoProfile) && !(v.VideoProfile == ProfileConstrainedBaseline && slices.Contains(s.Profiles, ProfileBaseline)) {
		return false
	}
	// Unknown source level (0) is trusted, as an unknown channel count is.
	if s.MaxLevel > 0 && v.VideoLevel > s.MaxLevel {
		return false
	}
	depths := s.BitDepths
	if len(depths) == 0 {
		depths = []int{8}
	}
	return slices.Contains(depths, v.VideoBitDepth)
}

// FormatSupport is one indivisible playback combination.
type FormatSupport struct {
	Transport Transport
	Video     VideoSupport
	Audio     AudioSupport
	Captions  []CaptionFormat
}

func (c Capabilities) CanPlayIn(container string, p ProbeInfo) bool {
	switch {
	case p.VideoCodec == "" && p.AudioCodec == "":
		return false
	case p.VideoInterlaced && !c.Deinterlaces:
		return false
	}
	return slices.ContainsFunc(c.Formats, func(f FormatSupport) bool {
		switch {
		case f.Transport.ContentType() != container:
			return false
		case p.VideoCodec != "" && !f.Video.accepts(p):
			return false
		case p.AudioCodec != "" && !f.Audio.accepts(p):
			return false
		default:
			return true
		}
	})
}

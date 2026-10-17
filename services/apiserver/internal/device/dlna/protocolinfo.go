package dlna

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"

	"github.com/huin/goupnp"

	"github.com/stupside/castor/services/apiserver/internal/device"
)

// capsTimeout bounds the ConnectionManager question a Connect asks.
const capsTimeout = 3 * time.Second

// sinkEntry is how the device itself names a container it accepts: the MIME and fourth field of its sink protocolInfo.
type sinkEntry struct {
	mime     string
	features string
}

// negotiateCaps asks the device what it accepts over ConnectionManager GetProtocolInfo, refusing a device that does not say.
func negotiateCaps(ctx context.Context, loc *goupnp.RootDevice, u *url.URL) (*mediav1.Capabilities, map[mediav1.Container]sinkEntry, error) {
	manager, err := findService(loc, u, "ConnectionManager")
	if err != nil {
		return nil, nil, fmt.Errorf("the device cannot say what it plays: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, capsTimeout)
	defer cancel()

	response := &struct{ Source, Sink string }{}
	if err := manager.SOAPClient.PerformActionCtx(
		ctx, manager.Service.ServiceType, "GetProtocolInfo", nil, response); err != nil {
		return nil, nil, fmt.Errorf("asking the device what it plays: %w", err)
	}
	caps, sinks := parseSinkProtocolInfo(response.Sink)
	switch {
	case len(caps.Video) == 0:
		return nil, nil, errors.New("the device advertises no H.264 or HEVC video in its sink")
	case len(caps.Audio) == 0:
		return nil, nil, errors.New("the device advertises no AAC, AC-3 or E-AC-3 audio in its sink")
	case len(caps.Containers) == 0:
		return nil, nil, errors.New("the device advertises neither MPEG-TS nor MP4 in its sink")
	}
	slog.InfoContext(ctx, "negotiated device capabilities", "codecs", codecs(caps.Video), "containers", caps.Containers)
	return caps, sinks, nil
}

// audioSupportFor builds the copy envelope for an audio codec.
func audioSupportFor(codec mediav1.AudioCodec) *mediav1.AudioSupport {
	if codec == mediav1.AudioCodec_AUDIO_CODEC_AAC {
		return &mediav1.AudioSupport{Codec: codec, MaxChannels: 2}
	}
	return &mediav1.AudioSupport{Codec: codec}
}

// The fixed order capabilities are reported in, so a given Sink always yields the same record.
var (
	discoverableCodecs      = []mediav1.VideoCodec{mediav1.VideoCodec_VIDEO_CODEC_H264, mediav1.VideoCodec_VIDEO_CODEC_HEVC}
	discoverableAudioCodecs = []mediav1.AudioCodec{mediav1.AudioCodec_AUDIO_CODEC_AAC, mediav1.AudioCodec_AUDIO_CODEC_AC3, mediav1.AudioCodec_AUDIO_CODEC_EAC3}
	discoverableContainers  = []mediav1.Container{mediav1.Container_CONTAINER_MPEGTS, mediav1.Container_CONTAINER_MP4}
)

// parseSinkProtocolInfo maps a ConnectionManager Sink protocolInfo CSV into capabilities and the device's own entry per container.
func parseSinkProtocolInfo(sink string) (*mediav1.Capabilities, map[mediav1.Container]sinkEntry) {
	present := map[mediav1.VideoCodec]bool{}
	audioPresent := map[mediav1.AudioCodec]bool{}
	sinks := map[mediav1.Container]sinkEntry{}
	for entry := range strings.SplitSeq(sink, ",") {
		fields := strings.SplitN(strings.TrimSpace(entry), ":", 4)
		if len(fields) < 3 || !strings.EqualFold(fields[0], "http-get") {
			continue
		}
		mime := strings.ToLower(fields[2])
		features := "*"
		if len(fields) == 4 {
			features = fields[3]
		}
		info := strings.ToUpper(features)
		codec, hasCodec := codecFromProfile(mime, info)
		if hasCodec {
			present[codec] = true
		}
		if c, ok := audioFromProfile(mime, info); ok {
			audioPresent[c] = true
		}
		// The first entry naming a codec speaks for its container, else the first there is.
		if ct, ok := containerFromMIME(mime); ok {
			if _, seen := sinks[ct]; !seen || hasCodec && !hasKnownCodec(sinks[ct]) {
				sinks[ct] = sinkEntry{mime: fields[2], features: features}
			}
		}
	}

	// A DLNA device deinterlaces: broadcast reaches it as fields.
	r := &mediav1.Capabilities{Deinterlaces: true, Captions: []mediav1.CaptionFormat{mediav1.CaptionFormat_CAPTION_FORMAT_SRT}}
	for _, c := range discoverableCodecs {
		if present[c] {
			r.Video = append(r.Video, device.VideoSupport(c))
		}
	}
	for _, c := range discoverableAudioCodecs {
		if audioPresent[c] {
			r.Audio = append(r.Audio, audioSupportFor(c))
		}
	}
	for _, ct := range discoverableContainers {
		if _, ok := sinks[ct]; ok {
			r.Containers = append(r.Containers, ct)
		}
	}
	if len(r.Containers) > 0 {
		r.ServedContainer = r.Containers[0]
	}
	return r, sinks
}

// hasKnownCodec is whether entry names a codec castor produces.
func hasKnownCodec(entry sinkEntry) bool {
	_, ok := codecFromProfile(strings.ToLower(entry.mime), strings.ToUpper(entry.features))
	return ok
}

// codecFromProfile reads the DLNA.ORG_PN token (already upper-cased), failing that the MIME type.
func codecFromProfile(mime, pn string) (mediav1.VideoCodec, bool) {
	switch {
	case strings.Contains(pn, "HEVC") || strings.Contains(pn, "H265") || strings.Contains(mime, "hevc") || strings.Contains(mime, "h265"):
		return mediav1.VideoCodec_VIDEO_CODEC_HEVC, true
	case strings.Contains(pn, "AVC") || strings.Contains(pn, "H264") || strings.Contains(mime, "avc") || strings.Contains(mime, "h264"):
		return mediav1.VideoCodec_VIDEO_CODEC_H264, true
	}
	return mediav1.VideoCodec_VIDEO_CODEC_UNSPECIFIED, false
}

func audioFromProfile(mime, pn string) (mediav1.AudioCodec, bool) {
	switch {
	case strings.Contains(pn, "EAC3") || strings.Contains(mime, "eac3") || strings.Contains(mime, "dd+"):
		return mediav1.AudioCodec_AUDIO_CODEC_EAC3, true
	case strings.Contains(pn, "AC3") || strings.Contains(mime, "ac3") || strings.Contains(mime, "dolby.dd"):
		return mediav1.AudioCodec_AUDIO_CODEC_AC3, true
	case strings.Contains(pn, "AAC") || strings.Contains(mime, "aac") || mime == "audio/mp4":
		return mediav1.AudioCodec_AUDIO_CODEC_AAC, true
	}
	return mediav1.AudioCodec_AUDIO_CODEC_UNSPECIFIED, false
}

// containerFromMIME reads the DLNA MIME spellings of the containers castor serves.
func containerFromMIME(mime string) (mediav1.Container, bool) {
	switch mime {
	case "video/mp2t", "video/vnd.dlna.mpeg-tts", "video/x-mpegts":
		return mediav1.Container_CONTAINER_MPEGTS, true
	case "video/mp4":
		return mediav1.Container_CONTAINER_MP4, true
	}
	return mediav1.Container_CONTAINER_UNSPECIFIED, false
}

// codecs names the codec of each support, video or audio.
func codecs[C interface {
	mediav1.VideoCodec | mediav1.AudioCodec
}, S interface{ GetCodec() C }](supports []S) []C {
	out := make([]C, len(supports))
	for i, s := range supports {
		out[i] = s.GetCodec()
	}
	return out
}

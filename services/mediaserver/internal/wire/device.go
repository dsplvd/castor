// Package wire translates castor's own types to the media contract and back, with no state and no I/O.
package wire

import (
	"errors"
	"net/url"

	"google.golang.org/protobuf/proto"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

var videoCodecs = map[mediav1.VideoCodec]media.Codec{
	mediav1.VideoCodec_VIDEO_CODEC_H264: media.CodecH264,
	mediav1.VideoCodec_VIDEO_CODEC_HEVC: media.CodecHEVC,
	mediav1.VideoCodec_VIDEO_CODEC_VP8:  media.CodecVP8,
}

var audioCodecs = map[mediav1.AudioCodec]media.Codec{
	mediav1.AudioCodec_AUDIO_CODEC_AAC:    media.CodecAAC,
	mediav1.AudioCodec_AUDIO_CODEC_AC3:    media.CodecAC3,
	mediav1.AudioCodec_AUDIO_CODEC_EAC3:   media.CodecEAC3,
	mediav1.AudioCodec_AUDIO_CODEC_MP3:    media.CodecMP3,
	mediav1.AudioCodec_AUDIO_CODEC_VORBIS: media.CodecVorbis,
}

var profiles = map[mediav1.Profile]media.Profile{
	mediav1.Profile_PROFILE_BASELINE:             media.ProfileBaseline,
	mediav1.Profile_PROFILE_CONSTRAINED_BASELINE: media.ProfileConstrainedBaseline,
	mediav1.Profile_PROFILE_MAIN:                 media.ProfileMain,
	mediav1.Profile_PROFILE_MAIN_10:              media.ProfileMain10,
	mediav1.Profile_PROFILE_HIGH:                 media.ProfileHigh,
}

// Container is contentType on the wire, unspecified for one the contract does not name.
func Container(contentType string) mediav1.Container {
	values := mediav1.Container_CONTAINER_UNSPECIFIED.Descriptor().Values()
	for i := range values.Len() {
		if c := mediav1.Container(values.Get(i).Number()); contentType != "" && mime(c) == contentType {
			return c
		}
	}
	return mediav1.Container_CONTAINER_UNSPECIFIED
}

// mime is the content type the contract names c by, empty for one it does not.
func mime(c mediav1.Container) string {
	v := c.Descriptor().Values().ByNumber(c.Number())
	if v == nil {
		return ""
	}
	return proto.GetExtension(v.Options(), mediav1.E_Mime).(string)
}

var captionFormats = map[mediav1.CaptionFormat]media.CaptionFormat{
	mediav1.CaptionFormat_CAPTION_FORMAT_VTT: media.CaptionVTT,
	mediav1.CaptionFormat_CAPTION_FORMAT_SRT: media.CaptionSRT,
}

// Caption is c on the wire, its URL as the device reaches it.
func Caption(c media.Caption, reached *url.URL) *mediav1.DeviceCommand_Caption {
	format := mediav1.CaptionFormat_CAPTION_FORMAT_VTT
	if c.Format == media.CaptionSRT {
		format = mediav1.CaptionFormat_CAPTION_FORMAT_SRT
	}
	return &mediav1.DeviceCommand_Caption{Url: reached.String(), Format: format, Language: c.Language, Name: c.Name, Default: c.Default}
}

// FromCapabilities is what the wire says a device decodes, without what this server does not know.
func FromCapabilities(c *mediav1.Capabilities) media.Capabilities {
	out := media.Capabilities{SelfFetch: c.GetSelfFetch(), Provenance: c.GetProvenance(), Deinterlaces: c.GetDeinterlaces(), ServedHeaders: c.GetServedHeaders()}
	for _, f := range c.GetFormats() {
		var transport media.Transport
		switch t := f.GetTransport().(type) {
		case *mediav1.FormatSupport_Progressive:
			transport = map[mediav1.ProgressiveSupport_Container]media.Transport{
				mediav1.ProgressiveSupport_CONTAINER_MP4:    media.TransportMP4,
				mediav1.ProgressiveSupport_CONTAINER_MPEGTS: media.TransportMPEGTS,
				mediav1.ProgressiveSupport_CONTAINER_MKV:    media.TransportMKV,
				mediav1.ProgressiveSupport_CONTAINER_WEBM:   media.TransportWebM,
			}[t.Progressive.GetContainer()]
		case *mediav1.FormatSupport_Hls:
			switch t.Hls.GetSubtitles() {
			case mediav1.HLSSupport_SUBTITLES_UNSPECIFIED:
				transport = media.TransportHLS
			case mediav1.HLSSupport_SUBTITLES_WEBVTT:
				transport = media.TransportHLSWebVTT
			}
		}
		if transport == 0 {
			continue
		}
		var video *mediav1.VideoSupport
		var audio *mediav1.AudioSupport
		switch tracks := f.GetTracks().(type) {
		case *mediav1.FormatSupport_Av:
			video, audio = tracks.Av.GetVideo(), tracks.Av.GetAudio()
			if video == nil || audio == nil {
				continue
			}
		case *mediav1.FormatSupport_Video:
			video = tracks.Video
		case *mediav1.FormatSupport_Audio:
			audio = tracks.Audio
		default:
			continue
		}
		envelope := media.FormatSupport{Transport: transport}
		if video != nil {
			v, ok := fromVideo(video)
			if !ok {
				continue
			}
			envelope.Video = v
		}
		if audio != nil {
			codec, ok := audioCodecs[audio.GetCodec()]
			if !ok {
				continue
			}
			envelope.Audio = media.AudioSupport{Codec: codec, MaxChannels: int(audio.GetMaxChannels())}
		}
		if envelope.Video.Codec == "" && envelope.Audio.Codec == "" {
			continue
		}
		for _, caption := range f.GetCaptions() {
			if format, ok := captionFormats[caption]; ok {
				envelope.Captions = append(envelope.Captions, format)
			}
		}
		out.Formats = append(out.Formats, envelope)
	}

	return out
}

func fromVideo(v *mediav1.VideoSupport) (media.VideoSupport, bool) {
	codec, ok := videoCodecs[v.GetCodec()]
	if !ok {
		return media.VideoSupport{}, false
	}
	vs := media.VideoSupport{Codec: codec, MaxLevel: int(v.GetMaxLevel())}
	for _, p := range v.GetProfiles() {
		if profile, ok := profiles[p]; ok {
			vs.Profiles = append(vs.Profiles, profile)
		}
	}
	// No profile left would read as any profile, wider than the device said.
	if len(v.GetProfiles()) > 0 && len(vs.Profiles) == 0 {
		return media.VideoSupport{}, false
	}
	for _, b := range v.GetBitDepths() {
		vs.BitDepths = append(vs.BitDepths, int(b))
	}
	return vs, true
}

// FromDeviceError is how the wire says a device failed, a *media.Gone for one that went away.
func FromDeviceError(e *mediav1.DeviceError) error {
	if g := e.GetGone(); g != nil {
		gone := &media.Gone{Device: g.GetDevice(), Observed: g.GetObserved()}
		if g.GetCause() != "" {
			gone.Err = errors.New(g.GetCause())
		}
		return gone
	}
	return errors.New(e.GetMessage())
}

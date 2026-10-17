package execute

import (
	"log/slog"
	"slices"

	"github.com/stupside/castor/services/mediaserver/internal/captions"
	"github.com/stupside/castor/services/mediaserver/internal/cast/deliver"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
)

// Captions is how a cast shows the text the source published, nil for a cast that wants none.
type Captions struct {
	Want   captions.Want
	Client source.Client
}

// shownCaptions fetches the caption the cast wants from what the source published and serves it for the device.
// A caption is a courtesy: whatever stops it is logged and the cast plays without.
func (s *pipeline) shownCaptions() []media.Caption {
	want := s.cast.Captions
	published := s.attempt.Rendition.Captions
	if want == nil || len(published) == 0 {
		return nil
	}
	format, ok := captionFormat(s.cast.Device.Capabilities())
	if !ok {
		slog.InfoContext(s.ctx, "the source publishes captions, but the device shows none")
		return nil
	}
	pick, ok := captions.Pick(published, want.Want)
	if !ok {
		slog.InfoContext(s.ctx, "the source publishes no caption in the language asked", "published", len(published))
		return nil
	}
	primary, _ := s.attempt.Program.PrimaryInput()
	vtt, err := captions.Fetch(s.ctx, want.Client, pick, primary.Headers)
	if err != nil {
		slog.WarnContext(s.ctx, "casting without the source's captions", "error", err)
		return nil
	}
	name, contentType, body := "captions.vtt", "text/vtt", vtt
	if format == media.CaptionSRT {
		name, contentType, body = "captions.srt", "text/srt", captions.SRT(vtt)
	}
	u, closeStatic, err := deliver.Static(s.ctx, s.cast.Listeners, name, contentType, body)
	if err != nil {
		slog.WarnContext(s.ctx, "casting without the source's captions", "error", err)
		return nil
	}
	s.releases.push(closeStatic)
	slog.InfoContext(s.ctx, "showing the source's captions", "language", pick.Language, "format", string(format))
	return []media.Caption{{URL: u, Format: format, Language: pick.Language, Name: pick.Name, Default: true}}
}

// captionFormat is the format a device takes captions in, WebVTT before SubRip.
func captionFormat(c media.Capabilities) (media.CaptionFormat, bool) {
	for _, f := range []media.CaptionFormat{media.CaptionVTT, media.CaptionSRT} {
		if slices.Contains(c.Captions, f) {
			return f, true
		}
	}
	return "", false
}

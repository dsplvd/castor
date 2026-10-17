package playlist

import (
	"fmt"
	"net/http"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/stupside/castor/e2e/origin"
	"github.com/stupside/castor/e2e/origin/serving"
	"github.com/stupside/castor/e2e/strategy"
)

// SourceCaptions builds a behaviour whose master offers a WebVTT rendition, as in `source-captions: {language: fr}`.
type SourceCaptions struct{}

func (SourceCaptions) Name() string { return "source-captions" }

func (SourceCaptions) Build(settings yaml.Node) (origin.Behaviour, error) {
	var c sourceCaptions
	if err := strategy.Decode(settings, &c); err != nil {
		return nil, fmt.Errorf("source-captions: %w", err)
	}
	if c.Language == "" || strings.ContainsAny(c.Language, "\",\r\n") {
		return nil, fmt.Errorf("source-captions: want the rendition's language as a plain tag")
	}
	return c, nil
}

type sourceCaptions struct {
	Language string `yaml:"language"`
}

func (sourceCaptions) Name() string { return "source-captions" }

// Cue is the text the rendition shows, which a judge looks for in what the receiver fetched.
const Cue = "source caption cue"

func (c sourceCaptions) Wrap(next http.Handler, _ origin.Published) http.Handler {
	masters := serving.RewritesPlaylists(next, func(playlist string) (string, bool) {
		if !strings.Contains(playlist, streamInf) || strings.Contains(playlist, "#EXT-X-MEDIA:TYPE=SUBTITLES") {
			return playlist, false
		}
		return c.rewrite(playlist), true
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/captions.m3u8":
			w.Header().Set("Content-Type", "application/x-mpegURL")
			fmt.Fprint(w, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:60\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXTINF:60,\ncaptions.vtt\n#EXT-X-ENDLIST\n")
		case "/captions.vtt":
			w.Header().Set("Content-Type", "text/vtt")
			fmt.Fprintf(w, "WEBVTT\n\n00:00:01.000 --> 00:00:03.000\n%s\n", Cue)
		default:
			masters.ServeHTTP(w, r)
		}
	})
}

func (c sourceCaptions) rewrite(master string) string {
	var out strings.Builder
	declared := false
	for line := range strings.Lines(master) {
		if strings.HasPrefix(line, streamInf) {
			if !declared {
				fmt.Fprintf(&out, `#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="subs",LANGUAGE="%s",NAME="%s",DEFAULT=YES,AUTOSELECT=YES,URI="captions.m3u8"`+"\n", c.Language, c.Language)
				declared = true
			}
			line = strings.TrimRight(line, "\r\n") + `,SUBTITLES="subs"` + "\n"
		}
		out.WriteString(line)
	}
	return out.String()
}

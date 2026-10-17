package playlist

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/stupside/castor/e2e/origin"
	"github.com/stupside/castor/e2e/origin/serving"
	"github.com/stupside/castor/e2e/strategy"
)

// AudioLanguages builds a behaviour offering the separate audio twice, as English (the default) and as another language, as in `audio-languages: {other: fr}`.
type AudioLanguages struct{}

func (AudioLanguages) Name() string { return "audio-languages" }

func (AudioLanguages) Build(settings yaml.Node) (origin.Behaviour, error) {
	var a audioLanguages
	if err := strategy.Decode(settings, &a); err != nil {
		return nil, fmt.Errorf("audio-languages: %w", err)
	}
	if a.Other == "" || a.Other == "en" || strings.ContainsAny(a.Other, "\",\r\n/") {
		return nil, fmt.Errorf("audio-languages: want the other language as a plain tag besides en")
	}
	return a, nil
}

type audioLanguages struct {
	Other string `yaml:"other"`
}

func (audioLanguages) Name() string { return "audio-languages" }

var (
	audioURI   = regexp.MustCompile(`URI="([^"]+)"`)
	audioGroup = regexp.MustCompile(`GROUP-ID="([^"]+)"`)
)

// A rendition is published under a prefix naming its language, which the origin strips, so a judge reads from the requests which one was fetched.
func (a audioLanguages) Wrap(next http.Handler, _ origin.Published) http.Handler {
	masters := serving.RewritesPlaylists(next, func(playlist string) (string, bool) {
		if !strings.Contains(playlist, "#EXT-X-MEDIA:TYPE=AUDIO") {
			return playlist, false
		}
		return a.rewrite(playlist), true
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, language := range []string{"en", a.Other} {
			if rest, ok := strings.CutPrefix(r.URL.Path, "/lang/"+language); ok {
				r = r.Clone(r.Context())
				r.URL.Path = rest
				break
			}
		}
		masters.ServeHTTP(w, r)
	})
}

func (a audioLanguages) rewrite(master string) string {
	var out strings.Builder
	for line := range strings.Lines(master) {
		if !strings.HasPrefix(line, "#EXT-X-MEDIA:TYPE=AUDIO") {
			out.WriteString(line)
			continue
		}
		uri, group := audioURI.FindStringSubmatch(line)[1], audioGroup.FindStringSubmatch(line)[1]
		fmt.Fprintf(&out, `#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="%s",LANGUAGE="en",NAME="en",DEFAULT=YES,AUTOSELECT=YES,URI="lang/en/%s"`+"\n", group, uri)
		fmt.Fprintf(&out, `#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="%s",LANGUAGE="%s",NAME="%s",DEFAULT=NO,AUTOSELECT=YES,URI="lang/%s/%s"`+"\n", group, a.Other, a.Other, a.Other, uri)
	}
	return out.String()
}

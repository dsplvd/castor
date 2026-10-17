package expect

import (
	"fmt"
	"path"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/stupside/castor/e2e/judge"
	"github.com/stupside/castor/e2e/strategy"
)

// AudioIn builds a check that castor read the rendition published under a language prefix, as in `audio-in: fr`.
type AudioIn struct{}

func (AudioIn) Name() string { return "audio-in" }

func (AudioIn) Build(settings yaml.Node) (judge.Check, error) {
	var language string
	if err := strategy.Decode(settings, &language); err != nil || language == "" {
		return nil, fmt.Errorf("audio-in: want the language castor must play (%v)", err)
	}
	return audioIn(language), nil
}

type audioIn string

func (audioIn) Name() string { return "audio-in" }

func (l audioIn) Judge(e judge.Evidence) []string {
	// The probe reads the default rendition up front, so the rendition read last is the one played.
	var played []string
	for _, r := range e.Origin.Requests() {
		rest, ok := strings.CutPrefix(r.Path, "/lang/")
		if language, _, found := strings.Cut(rest, "/"); ok && found && path.Ext(r.Path) != ".m3u8" {
			played = append(played, language)
		}
	}
	if len(played) == 0 || played[len(played)-1] != string(l) {
		return []string{fmt.Sprintf("castor played audio segments of %v, want its last read to be %s", played, l)}
	}
	return nil
}

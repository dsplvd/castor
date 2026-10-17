package expect

import (
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/stupside/castor/e2e/judge"
	"github.com/stupside/castor/e2e/origin/serving/playlist"
	"github.com/stupside/castor/e2e/strategy"
)

// Captioned builds a check that the receiver was handed the source's own text track, as in `captioned: vtt`.
type Captioned struct{}

func (Captioned) Name() string { return "captioned" }

func (Captioned) Build(settings yaml.Node) (judge.Check, error) {
	var format string
	if err := strategy.Decode(settings, &format); err != nil || (format != "vtt" && format != "srt") {
		return nil, fmt.Errorf("captioned: want the format the track must arrive in, vtt or srt (%v)", err)
	}
	return captioned(format), nil
}

type captioned string

func (captioned) Name() string { return "captioned" }

func (f captioned) Judge(e judge.Evidence) []string {
	for _, body := range e.Received.Captions {
		if strings.Contains(body, playlist.Cue) && strings.HasPrefix(body, "WEBVTT") == (f == "vtt") {
			return nil
		}
	}
	return []string{fmt.Sprintf("the receiver was handed no %s track carrying the source's cue", f)}
}

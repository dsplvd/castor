package expect

import (
	"testing"

	"github.com/stupside/castor/e2e/judge"
	"github.com/stupside/castor/e2e/origin"
)

func TestAudioLanguageJudgeReportsMissingRequests(t *testing.T) {
	got := audioIn("fr").Judge(judge.Evidence{Origin: &origin.Origin{}})
	if len(got) == 0 {
		t.Fatal("no audio requests must fail the audio-language expectation")
	}
}

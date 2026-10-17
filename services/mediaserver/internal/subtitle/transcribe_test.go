package subtitle

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestAgreedPrefix(t *testing.T) {
	prev := []Word{
		{Start: 1.0, End: 1.3, Text: "Hello,"},
		{Start: 1.4, End: 1.8, Text: "world"},
		{Start: 1.9, End: 2.2, Text: "again"},
	}
	cur := []Word{
		{Start: 1.1, End: 1.4, Text: "hello"}, // case/punct differ, times within tolerance
		{Start: 1.5, End: 1.9, Text: "world"},
		{Start: 2.0, End: 2.3, Text: "against"}, // text mismatch stops the prefix
	}
	got := agreedPrefix(prev, cur)
	if len(got) != 2 {
		t.Fatalf("agreedPrefix = %d words, want 2 (%v)", len(got), got)
	}
	if got[0].Text != "hello" {
		t.Errorf("committed word should carry the current hypothesis' surface form, got %q", got[0].Text)
	}
	if got := agreedPrefix(nil, cur); len(got) != 0 {
		t.Errorf("first hypothesis must commit nothing, got %v", got)
	}
}

func TestDropCommitted(t *testing.T) {
	words := []Word{
		{Start: 0.0, End: 0.5, Text: "old"},
		{Start: 0.6, End: 1.0, Text: "boundary"},
		{Start: 1.2, End: 1.6, Text: "new"},
	}
	got := dropCommitted(words, 1.0)
	if len(got) != 1 || got[0].Text != "new" {
		t.Fatalf("dropCommitted = %v, want just the word past the frontier", got)
	}
}

type recognitionFunc func(context.Context, []float32, float64, string) ([]Word, error)

func (f recognitionFunc) Recognize(ctx context.Context, samples []float32, offset float64, prompt string) ([]Word, error) {
	return f(ctx, samples, offset, prompt)
}

func TestTranscribePublishesSilentAudioFrontier(t *testing.T) {
	cues := &Cues{}
	pcm := bytes.NewReader(make([]byte, 7*bytesPerSec))
	r := recognitionFunc(func(context.Context, []float32, float64, string) ([]Word, error) { return nil, nil })
	if err := Transcribe(t.Context(), pcm, r, cues); err != nil {
		t.Fatal(err)
	}
	got := cues.Snapshot(0, 10)
	if got.SettledTo != 7 || !got.Complete || len(got.Cues) != 0 {
		t.Fatalf("silent transcription = %+v", got)
	}
}

func TestTranscribeDoesNotPublishFailedRecognitionAsSilence(t *testing.T) {
	cues := &Cues{}
	failed := errors.New("recognizer failed")
	r := recognitionFunc(func(context.Context, []float32, float64, string) ([]Word, error) { return nil, failed })
	if err := Transcribe(t.Context(), bytes.NewReader(make([]byte, 6*bytesPerSec)), r, cues); !errors.Is(err, failed) {
		t.Fatalf("error = %v", err)
	}
	got := cues.Snapshot(0, 10)
	if got.SettledTo != 0 || !got.Complete {
		t.Fatalf("failed transcription = %+v", got)
	}
}

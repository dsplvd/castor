package ffmpeg

import (
	"errors"
	"io"
	"testing"
)

type failedInput struct{ err error }

func (r failedInput) Read([]byte) (int, error) { return 0, r.err }

func TestProcessExitPreservesInputFailure(t *testing.T) {
	want := errors.New("input retention expired")
	p, err := Start(t.Context(), "sh", Command{Args: []string{"-c", "cat >/dev/null; exit 7"}}, Options{Stdin: failedInput{want}})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, p.Stdout)
	if err := p.Wait(); !errors.Is(err, want) {
		t.Fatalf("process exit erased input error: %v", err)
	}
	if p.Evidence().ExitStatus != 7 {
		t.Fatalf("exit = %d", p.Evidence().ExitStatus)
	}
}

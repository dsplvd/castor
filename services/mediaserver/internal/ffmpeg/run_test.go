package ffmpeg

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"testing"
	"time"
)

func TestRunPreservesCancellationCauseAndProcessFailure(t *testing.T) {
	path, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("sleep unavailable")
	}
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	cause := errors.New("measurement abandoned")
	timer := time.AfterFunc(50*time.Millisecond, func() { cancel(cause) })
	defer timer.Stop()
	_, err = Run(ctx, path, []string{"30"}, nil, io.Discard)
	if !errors.Is(err, cause) {
		t.Fatalf("Run error = %v, want cancellation cause", err)
	}
	if _, ok := errors.AsType[*exec.ExitError](err); !ok {
		t.Fatalf("Run discarded process failure: %v", err)
	}
}

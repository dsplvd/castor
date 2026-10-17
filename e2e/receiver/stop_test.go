package receiver

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

type uninterruptedViewer struct{}

func (uninterruptedViewer) Name() string { return "test" }
func (uninterruptedViewer) Watch(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithCancel(ctx)
}
func (uninterruptedViewer) Stopped(context.Context) bool { return false }
func (uninterruptedViewer) WillStop() bool               { return false }
func (uninterruptedViewer) Realtime() bool               { return false }
func (uninterruptedViewer) Pace(r io.Reader) io.Reader   { return r }

func TestSenderStopCancelsPlaybackAndIsNotSuccessfulCompletion(t *testing.T) {
	s := NewSession(t, Setup{Viewer: uninterruptedViewer{}})
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	s.stopPlayback = cancel
	s.Stop()
	s.Stop()
	if !errors.Is(context.Cause(ctx), errSenderStopped) {
		t.Fatal("STOP did not cancel playback")
	}
	if got := s.play(ctx, "http://example.invalid/video"); got != Stopped {
		t.Fatalf("stopped playback reported %v", got)
	}
	if len(s.received.Problems) != 0 {
		t.Fatalf("normal stop reported protocol errors: %v", s.received.Problems)
	}
}

func TestBufferedPlaybackDrainHonorsPauseAndStop(t *testing.T) {
	s := NewSession(t, Setup{})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s.Hold(true)
	done := make(chan struct{})
	go func() { s.drain(ctx, time.Now(), 0); close(done) }()
	select {
	case <-done:
		t.Fatal("paused playback drained")
	case <-time.After(40 * time.Millisecond):
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("STOP did not interrupt buffered playback")
	}
}

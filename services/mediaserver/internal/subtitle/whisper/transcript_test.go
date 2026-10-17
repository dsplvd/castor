package whisper

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/subtitle"
)

type blockedPCM struct {
	started    chan struct{}
	closed     chan struct{}
	closeCalls atomic.Int32
	signal     sync.Once
}

func (p *blockedPCM) Read([]byte) (int, error) {
	p.signal.Do(func() { close(p.started) })
	<-p.closed
	return 0, io.EOF
}
func (p *blockedPCM) Close() error {
	if p.closeCalls.Add(1) == 1 {
		close(p.closed)
	}
	return nil
}
func TestTranscriptCancellationClosesPCMExactlyOnce(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	pcm := &blockedPCM{started: make(chan struct{}), closed: make(chan struct{})}
	transcript := &Transcript{cues: &subtitle.Cues{}}
	finished := make(chan struct{})
	go func() { defer close(finished); transcript.Run(ctx, pcm) }()
	<-pcm.started
	cancel()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("transcription did not stop after cancellation")
	}
	if n := pcm.closeCalls.Load(); n != 1 {
		t.Fatalf("PCM closed %d times", n)
	}
	if !transcript.Done() {
		t.Fatal("cancellation did not finalize captions")
	}
}

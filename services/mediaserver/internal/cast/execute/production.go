package execute

import (
	"context"
	"errors"
	"io"
	"os"
	"sync"

	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// production owns an encoder and its output packager. Done publishes the final
// result only after packaging and process reaping have both finished.
type production struct {
	proc *ffmpeg.Process
	done chan struct{}
	stop func()
	err  error
}

func startProduction(parent context.Context, proc *ffmpeg.Process, input io.Closer, write func(context.Context, io.Reader) error) *production {
	ctx, cancel := context.WithCancel(parent)
	p := &production{proc: proc, done: make(chan struct{})}
	releaseIO := sync.OnceFunc(func() {
		if input != nil {
			_ = input.Close()
		}
		_ = proc.Stdout.Close()
	})
	interrupt := sync.OnceFunc(func() { proc.Kill(); releaseIO() })
	p.stop = func() { cancel(); interrupt() }
	stopped := context.AfterFunc(ctx, interrupt)
	go func() {
		defer close(p.done)
		defer cancel()
		defer func() { stopped(); interrupt() }()
		packaging := write(ctx, proc.Stdout)
		if packaging != nil {
			proc.Kill()
		}
		releaseIO()
		process := encoderResult(ctx, proc)
		if ctx.Err() != nil {
			packaging = interruptedPackaging(packaging)
		}
		p.err = errors.Join(packaging, process)
	}()
	return p
}

func (p *production) Done() <-chan struct{} { return p.done }
func (p *production) Err() error {
	select {
	case <-p.done:
		return p.err
	default:
		return nil
	}
}
func (p *production) Progress() media.Progress { return p.proc.Progress() }
func (p *production) Evidence() []string       { return p.proc.Evidence().Lines }
func (p *production) Close() error             { p.stop(); <-p.done; return p.err }

// Keep unrelated failures even when a packager joins them to interrupted I/O.
func interruptedPackaging(err error) error {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		var kept []error
		for _, child := range joined.Unwrap() {
			kept = append(kept, interruptedPackaging(child))
		}
		return errors.Join(kept...)
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		if child := wrapped.Unwrap(); child != nil && interruptedPackaging(child) == nil {
			return nil
		}
		return err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.ErrClosedPipe) || errors.Is(err, os.ErrClosed) {
		return nil
	}
	return err
}

package whisper

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

// workerProcess owns the child and all its pipes. exchange is serial; close is
// idempotent and always reaps the child before returning.
type workerProcess struct {
	input       io.WriteCloser
	output      io.ReadCloser
	diagnostics workerDiagnostics
	stop        func()
	completed   int // Native calls since startup; the recycling policy bounds leaks.
}

func startWorkerProcess(cmd *exec.Cmd) (*workerProcess, error) {
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		_ = input.Close()
		return nil, err
	}
	p := &workerProcess{input: input, output: output}
	cmd.Stderr = &p.diagnostics
	cmd.WaitDelay = 3 * time.Second
	if err := cmd.Start(); err != nil {
		_ = input.Close()
		_ = output.Close()
		return nil, err
	}
	p.stop = sync.OnceFunc(func() {
		_ = cmd.Process.Kill()
		_ = input.Close()
		_ = output.Close()
		_ = cmd.Wait()
	})
	return p, nil
}

func (p *workerProcess) exchange(ctx context.Context, req workerRequest) (workerResponse, error) {
	// Closing the subprocess pipes interrupts either synchronous IPC operation.
	// OnceFunc also joins an in-flight shutdown before diagnostics are inspected.
	stop := context.AfterFunc(ctx, p.stop)
	var response workerResponse
	err := writeFrame(p.input, req)
	if err == nil {
		err = readFrame(p.output, &response)
	}
	if !stop() {
		p.stop()
	}
	if cause := context.Cause(ctx); cause != nil {
		p.stop()
		return workerResponse{}, cause
	}
	if err != nil {
		p.stop()
		return workerResponse{}, fmt.Errorf("native worker failed: %w: %s\n...\n%s", err, p.diagnostics.head, p.diagnostics.tail)
	}
	p.completed++
	return response, nil
}

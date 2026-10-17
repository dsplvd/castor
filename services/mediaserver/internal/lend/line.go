// Package lend is a cast's hold on the device its lender connected: the line commands travel down, and the device the engine plays on.
package lend

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sync"
	"time"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
)

// ErrLenderLeft is every call on a line whose lender has left.
var ErrLenderLeft = errors.New("the API server that lent the device left")

// Line is a cast's one link to its device: the calls Drive sends down its stream, and those awaiting answers.
type Line struct {
	ctx           context.Context
	stop          context.CancelCauseFunc
	cancellations sync.WaitGroup
	// outbox hands each command to the Drive handler, the only goroutine that sends on its stream.
	outbox chan *mediav1.DeviceCommand

	mu       sync.Mutex
	awaiting map[string]chan *mediav1.AnswerRequest
}

func NewLine() *Line {
	ctx, stop := context.WithCancelCause(context.Background())
	return &Line{
		ctx: ctx, stop: stop,
		outbox:   make(chan *mediav1.DeviceCommand),
		awaiting: map[string]chan *mediav1.AnswerRequest{},
	}
}

// Outbox is each command to send down the lender's stream.
func (l *Line) Outbox() <-chan *mediav1.DeviceCommand { return l.outbox }

// Leave cuts the line: every call on it fails from now on.
func (l *Line) Leave() {
	l.stop(ErrLenderLeft)
	// Serialize with registration; no new worker starts after line cancellation.
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cancellations.Wait()
}

func (l *Line) Done() <-chan struct{} { return l.ctx.Done() }
func (l *Line) Err() error            { return context.Cause(l.ctx) }

const cancellationTimeout = 3 * time.Second

// Call has the device run cmd and waits for its answer; abandoning the wait cancels it there too.
func (l *Line) Call(ctx context.Context, call *mediav1.DeviceCommand_Call) (*mediav1.AnswerRequest, error) {
	answered := make(chan *mediav1.AnswerRequest, 1)
	id := rand.Text()
	l.mu.Lock()
	l.awaiting[id] = answered
	l.mu.Unlock()
	cmd := &mediav1.DeviceCommand{Command: &mediav1.DeviceCommand_Call_{Call: &mediav1.DeviceCommand_Call{Id: id, Operation: call.GetOperation()}}}
	defer l.forget(id)

	if err := l.send(ctx, cmd); err != nil {
		return nil, err
	}
	select {
	case a := <-answered:
		return a, nil
	case <-l.ctx.Done():
		return nil, context.Cause(l.ctx)
	case <-ctx.Done():
		// The line owns cancellation delivery after the caller leaves. Failure
		// terminates the line rather than silently leaving the remote call running.
		l.mu.Lock()
		if l.ctx.Err() == nil {
			l.cancellations.Go(func() {
				sending, cancel := context.WithTimeout(context.WithoutCancel(ctx), cancellationTimeout)
				defer cancel()
				if err := l.send(sending, &mediav1.DeviceCommand{Command: &mediav1.DeviceCommand_Cancel_{Cancel: &mediav1.DeviceCommand_Cancel{CommandId: id}}}); err != nil {
					l.stop(fmt.Errorf("%w: delivering cancellation: %w", ErrLenderLeft, err))
				}
			})
		}
		l.mu.Unlock()
		return nil, context.Cause(ctx)
	}
}

func (l *Line) send(ctx context.Context, cmd *mediav1.DeviceCommand) error {
	if err := context.Cause(ctx); err != nil {
		return err
	}
	if err := context.Cause(l.ctx); err != nil {
		return err
	}
	select {
	case l.outbox <- cmd:
		return nil
	case <-l.ctx.Done():
		return context.Cause(l.ctx)
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// Answer hands a to the call awaiting it, reporting whether one was.
func (l *Line) Answer(a *mediav1.AnswerRequest) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	answered, ok := l.awaiting[a.GetCommandId()]
	if ok {
		answered <- a
		delete(l.awaiting, a.GetCommandId())
	}
	return ok
}

func (l *Line) forget(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.awaiting, id)
}

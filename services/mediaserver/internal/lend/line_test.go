package lend

import (
	"buf.build/go/protovalidate"
	"context"
	"errors"
	"testing"
	"testing/synctest"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
)

func awaitEndCommand() *mediav1.DeviceCommand_Call {
	return &mediav1.DeviceCommand_Call{Operation: &mediav1.DeviceCommand_Call_AwaitEnd{AwaitEnd: &mediav1.DeviceCommand_AwaitEnd{}}}
}

// attached is a line whose commands arrive on the returned channel, as Drive would take them, until it is cut.
func attached(t *testing.T) (*Line, <-chan *mediav1.DeviceCommand) {
	t.Helper()
	l := NewLine()
	sent := make(chan *mediav1.DeviceCommand, 8)
	go func() {
		for {
			select {
			case cmd := <-l.outbox:
				sent <- cmd
			case <-l.Done():
				return
			}
		}
	}()
	return l, sent
}

func TestACallFailsOnceItsDeviceLeft(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l, sent := attached(t)
		answered := make(chan error, 1)
		go func() {
			_, err := l.Call(t.Context(), awaitEndCommand())
			answered <- err
		}()
		<-sent
		l.Leave()
		if err := <-answered; !errors.Is(err, ErrLenderLeft) {
			t.Errorf("a call awaiting a device that left ended with %v", err)
		}
		if _, err := l.Call(t.Context(), awaitEndCommand()); !errors.Is(err, ErrLenderLeft) {
			t.Errorf("a call after the device left ended with %v", err)
		}
		if len(sent) != 0 {
			t.Error("a command was sent after the device left")
		}
	})
}

func TestAnAbandonedCallIsCancelledOnTheDevice(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l, sent := attached(t)
		defer l.Leave()
		ctx, abandon := context.WithCancel(t.Context())
		go func() { _, _ = l.Call(ctx, awaitEndCommand()) }()
		cmd := <-sent
		abandon()
		if cancel := (<-sent).GetCancel(); cancel.GetCommandId() != cmd.GetCall().GetId() {
			t.Errorf("the device was sent %v, want the call cancelled", cancel)
		}
		synctest.Wait()
		if l.Answer(&mediav1.AnswerRequest{CommandId: cmd.GetCall().GetId()}) {
			t.Error("an abandoned call still awaited its answer")
		}
	})
}

func TestCommandIdentityBelongsOnlyToCalls(t *testing.T) {
	valid := []*mediav1.DeviceCommand{
		{Command: &mediav1.DeviceCommand_Call_{Call: &mediav1.DeviceCommand_Call{Id: "call-1", Operation: &mediav1.DeviceCommand_Call_AwaitEnd{AwaitEnd: &mediav1.DeviceCommand_AwaitEnd{}}}}},
		{Command: &mediav1.DeviceCommand_Cancel_{Cancel: &mediav1.DeviceCommand_Cancel{CommandId: "call-1"}}},
	}
	for _, command := range valid {
		if err := protovalidate.Validate(command); err != nil {
			t.Fatal(err)
		}
	}
	invalid := []*mediav1.DeviceCommand{
		{},
		{Command: &mediav1.DeviceCommand_Call_{Call: &mediav1.DeviceCommand_Call{Operation: &mediav1.DeviceCommand_Call_AwaitEnd{AwaitEnd: &mediav1.DeviceCommand_AwaitEnd{}}}}},
		{Command: &mediav1.DeviceCommand_Call_{Call: &mediav1.DeviceCommand_Call{Id: "call-1"}}},
		{Command: &mediav1.DeviceCommand_Cancel_{Cancel: &mediav1.DeviceCommand_Cancel{}}},
	}
	for _, command := range invalid {
		if err := protovalidate.Validate(command); err == nil {
			t.Fatalf("incomplete command accepted: %v", command)
		}
	}
}

func TestUndeliverableCancellationTerminatesLine(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := NewLine()
		defer l.Leave()
		ctx, cancel := context.WithCancelCause(t.Context())
		abandoned := errors.New("viewer abandoned request")
		returned := make(chan error, 1)
		go func() { _, err := l.Call(ctx, awaitEndCommand()); returned <- err }()
		<-l.Outbox() // The lender accepts the call, then stops consuming commands.
		cancel(abandoned)
		if err := <-returned; !errors.Is(err, abandoned) {
			t.Fatalf("lost caller cause: %v", err)
		}
		<-l.Done()
		if !errors.Is(l.Err(), ErrLenderLeft) || !errors.Is(l.Err(), context.DeadlineExceeded) {
			t.Fatalf("lost cancellation delivery failure: %v", l.Err())
		}
		if _, err := l.Call(t.Context(), awaitEndCommand()); !errors.Is(err, ErrLenderLeft) {
			t.Fatalf("terminated line accepted another call: %v", err)
		}
	})
}

func TestLeaveJoinsPendingCancellationDelivery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := NewLine()
		ctx, cancel := context.WithCancel(t.Context())
		returned := make(chan struct{})
		go func() { defer close(returned); _, _ = l.Call(ctx, awaitEndCommand()) }()
		<-l.Outbox()
		cancel()
		<-returned
		l.Leave()
		l.Leave() // Both ownership release and cancellation failure may end the line.
		if !errors.Is(l.Err(), ErrLenderLeft) {
			t.Fatal(l.Err())
		}
	})
}

func TestCanceledCallCannotDispatchToReadyOutbox(t *testing.T) {
	l := NewLine()
	defer l.Leave()
	// A buffered outbox makes dispatch immediately ready, exercising the select
	// race that previously could send a command after cancellation.
	l.outbox = make(chan *mediav1.DeviceCommand, 1)
	ctx, cancel := context.WithCancelCause(t.Context())
	failure := errors.New("call already canceled")
	cancel(failure)
	if _, err := l.Call(ctx, awaitEndCommand()); !errors.Is(err, failure) {
		t.Fatalf("call=%v", err)
	}
	if len(l.outbox) != 0 {
		t.Fatal("canceled call reached outbox")
	}
	l.Leave()
	if _, err := l.Call(t.Context(), awaitEndCommand()); !errors.Is(err, ErrLenderLeft) {
		t.Fatalf("closed call=%v", err)
	}
	if len(l.outbox) != 0 {
		t.Fatal("call dispatched after line left")
	}
}

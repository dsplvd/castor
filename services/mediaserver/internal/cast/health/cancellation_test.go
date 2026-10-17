package health

import (
	"context"
	"errors"
	"testing"
)

func TestCanceledWatchDoesNotReadResources(t *testing.T) {
	cause := errors.New("cast owner stopped")
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(cause)
	err := Watch(ctx, Monitor{
		Phase: Opening,
		Landed: func() int64 {
			t.Fatal("read a resource after cancellation")
			return 0
		},
	})
	if !errors.Is(err, cause) {
		t.Fatalf("watch error = %v, want owner cause", err)
	}
}

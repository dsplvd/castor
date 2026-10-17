package follow

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

func TestACancelledResourceCallerDoesNotWaitForTheSharedRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var cache resources[string]
		started, release := make(chan struct{}), make(chan struct{})
		ctx, cancel := context.WithCancel(t.Context())
		finished := make(chan error, 1)
		go func() {
			_, err := cache.get(ctx, t.Context(), "key", func(context.Context) ([]byte, error) {
				close(started)
				<-release
				return []byte("secret"), nil
			})
			finished <- err
		}()
		<-started
		cancel()
		synctest.Wait()
		select {
		case err := <-finished:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("get = %v, want cancellation", err)
			}
		default:
			t.Error("the cancelled caller is still waiting on the shared read")
		}
		close(release)
		synctest.Wait()
		b, err := cache.get(t.Context(), t.Context(), "key", func(context.Context) ([]byte, error) {
			t.Error("the successful shared read was not cached")
			return nil, errors.New("unexpected second read")
		})
		if err != nil || string(b) != "secret" {
			t.Errorf("cached key = %q (%v)", b, err)
		}
	})
}

func TestResourceCacheBoundsBytes(t *testing.T) {
	var cache resources[int]
	for i := range 10 {
		_, err := cache.get(t.Context(), t.Context(), i, func(context.Context) ([]byte, error) { return make([]byte, 8<<20), nil })
		if err != nil {
			t.Fatal(err)
		}
	}
	bytes := 0
	for _, value := range cache.held {
		bytes += len(value)
	}
	if bytes > resourceBytes || len(cache.held) != 4 {
		t.Fatalf("bytes=%d entries=%d", bytes, len(cache.held))
	}
}

func TestResourceReadEndsWithItsFeed(t *testing.T) {
	lifetime, cancel := context.WithCancelCause(t.Context())
	cause := errors.New("republisher stopped")
	var cache resources[string]
	started := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		_, err := cache.get(t.Context(), lifetime, "key", func(ctx context.Context) ([]byte, error) {
			close(started)
			<-ctx.Done()
			return nil, context.Cause(ctx)
		})
		finished <- err
	}()
	<-started
	cancel(cause)
	select {
	case err := <-finished:
		if !errors.Is(err, cause) {
			t.Fatalf("read error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("resource read outlived its feed")
	}
}

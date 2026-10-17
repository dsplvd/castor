package cast

import (
	"context"
	"testing"
	"testing/synctest"

	"connectrpc.com/connect"
	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/services/mediaserver/internal/source"
)

type drainingResolver struct {
	retrying
	entered chan struct{}
	release chan struct{}
}

func (r drainingResolver) Measure(ctx context.Context, _ *source.Stream) (*source.Stream, error) {
	close(r.entered)
	<-ctx.Done()
	<-r.release
	return nil, context.Cause(ctx)
}

func TestDrainCancelsAndJoinsResolutionRequests(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := drainingResolver{entered: make(chan struct{}), release: make(chan struct{})}
		s := New(func(*mediav1.PlaybackSettings) Caster { return r }, nil)
		req := &mediav1.RankRequest{Source: &mediav1.Source{Source: &mediav1.Source_Stream{Stream: &castorv1.Stream{Url: "https://example.com/video"}}}}
		result := make(chan error, 1)
		go func() { _, err := s.Rank(t.Context(), req); result <- err }()
		<-r.entered
		drained := make(chan struct{})
		go func() { s.Drain(t.Context()); close(drained) }()
		synctest.Wait()
		select {
		case <-drained:
			t.Fatal("shutdown returned before resolution cleanup")
		default:
		}
		if _, err := s.Rank(t.Context(), req); connect.CodeOf(err) != connect.CodeUnavailable {
			t.Fatalf("new resolution during shutdown: %v", err)
		}
		close(r.release)
		<-drained
		if err := <-result; connect.CodeOf(err) != connect.CodeUnavailable {
			t.Fatalf("interrupted resolution: %v", err)
		}
	})
}

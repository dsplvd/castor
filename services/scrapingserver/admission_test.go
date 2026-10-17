package scrapingserver

import (
	"context"
	"testing"
	"testing/synctest"

	"connectrpc.com/connect"
	scrapingv1 "github.com/stupside/castor/gen/castor/scraping/v1"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
)

func TestScrapingRejectsExcessRequestsAndReusesCapacity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		finish := make(chan struct{})
		entered := make(chan struct{}, 16)
		s := New(resolveFunc(func(context.Context, []string) ([]*castorv1.StreamCandidate, error) {
			entered <- struct{}{}
			<-finish
			return []*castorv1.StreamCandidate{{Stream: &castorv1.Stream{Url: "https://cdn.example/video.mp4"}}}, nil
		}))
		defer s.Shutdown(t.Context())
		done := make(chan error, 16)
		for range 16 {
			go func() { _, err := s.Resolve(t.Context(), &scrapingv1.ResolveRequest{}); done <- err }()
		}
		synctest.Wait()
		if len(entered) != 16 {
			t.Fatal("requests did not reach resolver")
		}
		if _, err := s.Resolve(t.Context(), &scrapingv1.ResolveRequest{}); connect.CodeOf(err) != connect.CodeResourceExhausted {
			t.Fatalf("excess request = %v", err)
		}
		close(finish)
		for range 16 {
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			<-entered
		}
		if _, err := s.Resolve(t.Context(), &scrapingv1.ResolveRequest{}); err != nil {
			t.Fatal("capacity not released", err)
		}
	})
}

func TestShutdownTimeoutCanBeRetriedUntilResolverReturns(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		finish := make(chan struct{})
		s := New(resolveFunc(func(ctx context.Context, _ []string) ([]*castorv1.StreamCandidate, error) {
			<-finish
			return nil, ctx.Err()
		}))
		requestDone := make(chan struct{})
		go func() { defer close(requestDone); _, _ = s.Resolve(t.Context(), &scrapingv1.ResolveRequest{}) }()
		synctest.Wait()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		s.Shutdown(ctx)
		if _, err := s.Resolve(t.Context(), &scrapingv1.ResolveRequest{}); connect.CodeOf(err) != connect.CodeUnavailable {
			t.Fatal(err)
		}
		drained := make(chan struct{})
		go func() { s.Shutdown(t.Context()); close(drained) }()
		synctest.Wait()
		select {
		case <-drained:
			t.Fatal("shutdown returned before resolver cleanup")
		default:
		}
		close(finish)
		<-requestDone
		<-drained
		s.Shutdown(t.Context())
	})
}

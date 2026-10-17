package transport

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"
)

type failedListener struct{ err error }

func (l failedListener) Accept() (net.Conn, error) { return nil, l.err }
func (failedListener) Close() error                { return nil }
func (failedListener) Addr() net.Addr              { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1234} }

func TestListenerFailureStillDrainsServerWork(t *testing.T) {
	failure := errors.New("listener failed")
	drained := false
	err := Serve(t.Context(), failedListener{failure}, http.NotFoundHandler(), func(ctx context.Context) {
		if ctx.Err() != nil {
			t.Error("cleanup was not given a usable grace context")
		}
		drained = true
	})
	if !errors.Is(err, failure) {
		t.Fatalf("Serve = %v, want the listener failure", err)
	}
	if !drained {
		t.Fatal("the listener failed and abandoned running server work")
	}
}

func TestShutdownForcesConnectionsClosedBeforeJoiningDrain(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	ended := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	served := make(chan error, 1)
	go func() {
		served <- Serve(ctx, listener, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(entered)
			select {
			case <-r.Context().Done():
			case <-release:
			}
			close(ended)
		}), func(context.Context) { <-ended })
	}()
	clientDone := make(chan struct{})
	go func() {
		defer close(clientDone)
		resp, err := http.Get("http://" + listener.Addr().String())
		if err == nil {
			resp.Body.Close()
		}
	}()
	<-entered
	cancel()
	select {
	case err := <-served:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("shutdown discarded its deadline: %v", err)
		}
	case <-time.After(shutdownGrace + 3*time.Second):
		t.Fatal("drain waited on a connection that shutdown never closed")
	}
	<-clientDone
}

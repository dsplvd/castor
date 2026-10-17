// Package httpserve owns HTTP listener startup and bounded shutdown for deliveries.
package httpserve

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"
)

const PollInterval = 500 * time.Millisecond

// Start answers the device on ln until the returned server closes; its requests carry ctx's values, not its end.
func Start(ctx context.Context, ln net.Listener, h http.Handler) *http.Server {
	base := context.WithoutCancel(ctx)
	server := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second, BaseContext: func(net.Listener) context.Context { return base }}
	go func() {
		if err := server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.ErrorContext(base, "delivery HTTP server failed", "error", err)
		}
	}()
	return server
}

// closeGrace is how long a closing delivery lets the responses it already wrote finish going out.
const closeGrace = 2 * time.Second

// Stop ends server once its responses have gone out whole, cutting what still runs after closeGrace.
func Stop(server *http.Server) error {
	ctx, cancel := context.WithTimeout(context.Background(), closeGrace)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		return errors.Join(err, server.Close())
	}
	return nil
}

// Settle waits for drained, then until finished holds, re-reading it every PollInterval.
func Settle(ctx context.Context, drained <-chan struct{}, finished func() bool) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-drained:
	}
	tick := time.NewTicker(PollInterval)
	defer tick.Stop()
	for !finished() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
	return nil
}

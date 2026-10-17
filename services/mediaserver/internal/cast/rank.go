package cast

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
)

// Rank readies a source's streams as a cast asked the same would, casting nothing.
func (s *Service) Rank(ctx context.Context, req *mediav1.RankRequest) (*mediav1.RankResponse, error) {
	if s.ctx.Err() != nil {
		return nil, connect.NewError(connect.CodeUnavailable, errShutdown)
	}
	if !s.resolutions.TryAcquire(1) {
		return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("concurrent resolutions capacity exhausted"))
	}
	defer s.resolutions.Release(1)
	if s.ctx.Err() != nil {
		return nil, connect.NewError(connect.CodeUnavailable, errShutdown)
	}
	ctx, cancel := context.WithCancelCause(ctx)
	stop := context.AfterFunc(s.ctx, func() { cancel(errShutdown) })
	defer stop()
	defer cancel(nil)
	src := originOf(req.GetSource())
	found, err := src.streams()
	if err != nil {
		return nil, failed(ctx, connect.CodeNotFound, err)
	}
	ranked, err := src.ready(ctx, s.caster(req.GetSettings()), found)
	if err != nil {
		return nil, failed(ctx, connect.CodeFailedPrecondition, err)
	}
	out := &mediav1.RankResponse{Ranked: make([]*castorv1.RankedStream, len(ranked))}
	for i, r := range ranked {
		out.Ranked[i] = &castorv1.RankedStream{Url: r.URL.String(), LastResort: r.LastResort}
		if bitrate := r.Bitrate(); bitrate > 0 {
			out.Ranked[i].Bitrate = new(uint64(bitrate))
		}
	}
	return out, nil
}

// failed codes err as code, unless the request itself ended, which connect codes from its context.
func failed(ctx context.Context, code connect.Code, err error) error {
	if errors.Is(context.Cause(ctx), errShutdown) {
		return connect.NewError(connect.CodeUnavailable, errShutdown)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return connect.NewError(code, err)
}

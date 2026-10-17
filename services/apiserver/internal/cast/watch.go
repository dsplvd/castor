package cast

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
)

// Watch sends the cast's status at once and on every change, and how it ended last.
func (s *Service) Watch(ctx context.Context, req *castorv1.WatchRequest, out *connect.ServerStream[castorv1.WatchResponse]) error {
	if !s.watchers.TryAcquire(1) {
		return connect.NewError(connect.CodeResourceExhausted, errors.New("concurrent watchers capacity exhausted"))
	}
	defer s.watchers.Release(1)
	c, err := s.find(req.GetCastId())
	if err != nil {
		return err
	}
	var sent *castorv1.CastStatus
	for {
		now, changed := c.now.Load()
		if !proto.Equal(now.status, sent) {
			if err := out.Send(&castorv1.WatchResponse{Update: &castorv1.WatchResponse_Status{Status: now.status}}); err != nil {
				return err
			}
			sent = now.status
		}
		if now.ended != nil {
			return out.Send(&castorv1.WatchResponse{Update: &castorv1.WatchResponse_Ended{Ended: now.ended}})
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

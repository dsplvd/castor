package cast

import (
	"context"
	"log/slog"

	"google.golang.org/protobuf/proto"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
)

// progress is how many streams a status has found and, once ranked, how many are castable.
func progress(s *castorv1.CastStatus) (streams uint32, castable *uint32) {
	switch state := s.GetState().(type) {
	case *castorv1.CastStatus_Measuring:
		return state.Measuring.GetStreams(), state.Measuring.Castable
	case *castorv1.CastStatus_Casting:
		return state.Casting.GetStreams(), new(state.Casting.GetCastable())
	}
	return 0, nil
}

// changed logs what moved between two statuses of a cast.
func changed(ctx context.Context, last, now *castorv1.CastStatus) {
	switch state := now.GetState().(type) {
	case *castorv1.CastStatus_Connecting:
		if _, same := last.GetState().(*castorv1.CastStatus_Connecting); !same {
			slog.InfoContext(ctx, "cast connecting the device")
		}
	case *castorv1.CastStatus_Extracting:
		if _, same := last.GetState().(*castorv1.CastStatus_Extracting); !same {
			slog.InfoContext(ctx, "cast finding streams")
		}
	case *castorv1.CastStatus_Measuring, *castorv1.CastStatus_Casting:
		streams, castable := progress(now)
		was, wasCastable := progress(last)
		if streams != was {
			slog.InfoContext(ctx, "cast measuring", "streams", streams)
		}
		if castable != nil && (wasCastable == nil || *castable != *wasCastable) {
			slog.InfoContext(ctx, "cast measured", "castable", *castable)
		}
		if casting, ok := state.(*castorv1.CastStatus_Casting); ok {
			previous := last.GetCasting()
			if revision := casting.Casting.GetRevision(); revision != nil && !proto.Equal(revision, previous.GetRevision()) {
				slog.WarnContext(ctx, "cast revising", "action", revision.GetAction().String(), "why", revision.GetWhy())
			}
			if casting.Casting.GetAttempt() != previous.GetAttempt() {
				slog.InfoContext(ctx, "cast attempting", "try", casting.Casting.GetAttempt())
			}
			if casting.Casting.GetPlaying() && !previous.GetPlaying() {
				slog.InfoContext(ctx, "cast playing on the device", "try", casting.Casting.GetAttempt())
			}
		}
	}
}

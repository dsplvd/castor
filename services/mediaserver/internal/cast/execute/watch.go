package execute

import (
	"context"
	"errors"

	"github.com/stupside/castor/services/mediaserver/internal/cast/health"
)

// gate holds a buffered cast until its read has proven it can deliver, and its transcription leads.
func gate(ctx context.Context, buf *buffered) error {
	return health.Watch(ctx, readMonitor(buf.reader, health.Monitor{
		Subject: "playback gate",
		Phase:   health.Reading,
		Lead:    buf.transcript,
	}))
}

// awaitArtifact waits for the artifact the device will fetch to exist.
func awaitArtifact(ctx context.Context, d delivery) error {
	artifact := d.sink.Artifact()
	return health.Watch(ctx, health.Monitor{
		Subject:  artifact.Subject,
		Phase:    health.Opening,
		Producer: d.output,
		Landed:   artifact.Landed,
		Grace:    artifact.Grace,
	})
}

// supervising watches a served cast until its delivery, device or verdict ends it, reporting whether the delivery ran its course.
func supervising(ctx context.Context, dev Device, d delivery, f feed) (bool, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	delivered := make(chan error, 1)
	go func(result chan<- error) { result <- d.sink.Wait(ctx) }(delivered)
	playback := make(chan error, 1)
	go func(result chan<- error) { result <- dev.AwaitEnd(ctx) }(playback)

	// Nil where delivery has no supervisor; sink deleting behind its live edge reads as stall on every cast.
	var judged chan error
	if aud := d.sink.Audience(); aud != nil {
		judged = make(chan error, 1)
		go func(result chan<- error) { result <- health.Watch(ctx, playingMonitor(f, d, aud, dev)) }(judged)
	}

	// The first completion ends playback, but every worker belongs to this scope.
	// Join them before the caller tears down the resources they observe.
	var err error
	var completed bool
	select {
	case err = <-delivered:
		completed = err == nil
		delivered = nil
	case err = <-judged:
		judged = nil
	case err = <-playback:
		playback = nil
	}
	cancel()
	if delivered != nil {
		local := <-delivered
		completed = local == nil
		err = errors.Join(err, unlessTeardown(local))
	}
	if judged != nil {
		err = errors.Join(err, unlessTeardown(<-judged))
	}
	if playback != nil {
		err = errors.Join(err, unlessTeardown(<-playback))
	}
	return completed, err
}

// Ignore cancellation caused by joining workers, but retain failures joined to it.
func unlessTeardown(err error) error {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		var kept []error
		for _, child := range joined.Unwrap() {
			kept = append(kept, unlessTeardown(child))
		}
		return errors.Join(kept...)
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		if child := wrapped.Unwrap(); child != nil && unlessTeardown(child) == nil {
			return nil
		}
		return err
	}
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// readMonitor judges a buffered cast by its read.
func readMonitor(reader *pull, m health.Monitor) health.Monitor {
	m.Producer = reader
	m.Telemetry = reader
	m.Landed = reader.spool.Size
	return m
}

// playingMonitor judges a buffered cast by its read, and one reading its source by the encoder serving it.
func playingMonitor(f feed, d delivery, aud health.Audience, dev Device) health.Monitor {
	m := health.Monitor{Subject: "the playing cast", Phase: health.Playing, Audience: aud}
	if f.buffered != nil {
		// Only a read that can get ahead of the device is worth holding the device for.
		if !f.buffered.live {
			m.Transport = dev
		}
		return readMonitor(f.buffered.reader, m)
	}
	m.Producer, m.Telemetry = d.output, d.output
	m.Landed = d.sink.Artifact().Landed
	return m
}

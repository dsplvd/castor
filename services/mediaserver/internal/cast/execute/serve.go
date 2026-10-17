package execute

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/deliver"
	"github.com/stupside/castor/services/mediaserver/internal/cast/health"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// feed is what a served cast's encoder reads: the read's buffer where there is one, else the program's source.
type feed struct {
	buffered *buffered
	program  media.Program
}

// spliced is whether the plan joins a seamed program itself; a buffer was made one stream by the floor.
func (f feed) spliced() bool { return f.buffered == nil && f.program.Seamed() }

// delivery is what serves a cast: the mechanism the device fetches from, and what writes the bytes it serves.
type delivery struct {
	sink   output.Output
	output producer
}

// producer is what writes the served bytes, judged by the media it has made rather than bytes a muxer pads.
type producer interface {
	health.Producer
	health.Telemetry
}

// serve encodes what f reads for the device, delivers it, and watches it play; it reports how far the delivery got.
func (s *pipeline) serve(work string, f feed, burn burnRenderer) (health.Phase, error) {
	opts, err := s.encode(f, burn)
	if err != nil {
		return health.Unstarted, err
	}
	d, err := s.produce(work, f, opts, burn)
	if err != nil {
		return health.Unstarted, err
	}
	if err := awaitArtifact(s.ctx, d); err != nil {
		return health.Opening, err
	}
	_, videoEncoded := opts.Video.Encode()
	_, audioEncoded := opts.Audio.Encode()
	if videoEncoded || audioEncoded {
		if err := s.validateOutput(d, opts); err != nil {
			return health.Opening, err
		}
	}
	if err := hand(s.ctx, s.cast.Device, d.sink.URL(), opts.Format.ContentType, s.shown, false); err != nil {
		return health.Opening, err
	}
	delivered, err := supervising(s.ctx, s.cast.Device, d, f)
	if err != nil {
		return health.Playing, err
	}
	if !delivered {
		if made, sent, ok := starved(f, d); ok {
			return health.Playing, health.DeviceStarved("the playing cast", made, sent)
		}
		return health.Playing, nil
	}
	return health.Playing, d.sink.Settled()
}

// validateOutput checks the encoder's actual result, not just its requested flags.
// Probing local artifacts never consumes the receiver's HTTP delivery counters.
func (s *pipeline) validateOutput(d delivery, opts encoding) error {
	path := d.sink.Artifact().Path
	if path == "" {
		return fmt.Errorf("encoded delivery has no local artifact to validate")
	}
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()
	return validateEncoded(ctx, s.cast.Probes.File(path), opts.delivery.Capabilities, opts.Format.ContentType, opts.Probe, d.output.Done())
}

func validateEncoded(ctx context.Context, prober media.Prober, caps media.Capabilities, contentType string, expected media.ProbeInfo, done <-chan struct{}) error {
	completed := false
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("validating encoded output: %w", err)
		}
		measured, _, err := prober.Probe(ctx)
		if cause := ctx.Err(); cause != nil {
			return fmt.Errorf("validating encoded output: %w", cause)
		}
		if err == nil && (expected.VideoCodec == "" || measured.VideoCodec != "") && (expected.AudioCodec == "" || measured.AudioCodec != "") {
			// Container headers can identify tracks and dimensions before the first
			// video packet supplies pixel format / codec parameters. A successful
			// ffprobe exit alone does not make that growing artifact complete.
			incomplete := measured.VideoCodec != "" && (measured.VideoBitDepth == 0 ||
				((measured.VideoCodec == media.CodecH264 || measured.VideoCodec == media.CodecHEVC) && (measured.VideoProfile == "" || measured.VideoLevel < 0)))
			switch {
			case incomplete:
				err = fmt.Errorf("encoded artifact does not yet contain video codec parameters")
			case !caps.CanPlayIn(contentType, measured):
				return fmt.Errorf("encoded output does not satisfy receiver capabilities: video=%s profile=%s level=%d audio=%s", measured.VideoCodec, measured.VideoProfile, measured.VideoLevel, measured.AudioCodec)
			default:
				return nil
			}
		}
		if err == nil {
			err = fmt.Errorf("encoded artifact does not yet contain all expected tracks")
		}
		if completed {
			return fmt.Errorf("validating completed encoded output: %w", err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("validating encoded output: %w", ctx.Err())
		case <-done:
			// The probe may have raced the producer's last write. Observe the final
			// artifact once before deciding it is permanently incomplete.
			completed = true
			done = nil
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (s *pipeline) produce(work string, f feed, opts encoding, burn burnRenderer) (delivery, error) {
	o := deliver.Opening{
		Live:          f.buffered != nil && f.buffered.live || slices.ContainsFunc(f.program.Inputs, func(in media.Input) bool { return in.Fetching().Live }),
		Format:        opts.Format,
		Listeners:     s.cast.Listeners,
		Headers:       s.cast.Device.Capabilities().ServedHeaders,
		IdleGrace:     idleGrace,
		WriteDeadline: writeDeadline,
	}
	// A burn-in follows the encoder's progress, so only a cast without one can do without an encoder.
	if f.buffered != nil && burn == nil && opts.Verbatim() {
		if relay, ok := opts.delivery.Plan.(output.Relayer); ok {
			return s.relay(relay, o, f.buffered.reader)
		}
	}

	var tail io.ReadCloser
	if f.buffered != nil {
		var err error
		if tail, err = f.buffered.reader.spool.TailAt(s.ctx, 0); err != nil {
			return delivery{}, err
		}
	}
	closeTail := func() {
		if tail != nil {
			_ = tail.Close()
		}
	}

	dir := filepath.Join(work, "delivery")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		closeTail()
		return delivery{}, fmt.Errorf("creating the delivery directory: %w", err)
	}

	reservation, err := opts.delivery.Plan.Reserve(dir)
	if err != nil {
		closeTail()
		return delivery{}, err
	}
	if reservation != nil {
		s.releases.push(reservation)
	}
	encoderOpts := opts.EncodeOptions
	encoderOpts.Format = opts.delivery.Plan.EncoderFormat()
	var step func(media.Progress)
	if burn != nil {
		step = burn.Writer(s.ctx)
	}
	proc, err := s.startEncoder(encoderOpts, tail, step, dir)
	if err != nil {
		closeTail()
		return delivery{}, err
	}

	o.Abort = proc.Kill
	sk, err := opts.delivery.Plan.Open(s.ctx, o, dir, proc.Stdout, proc.Progress)
	// Killed first, since everything after needs it to have stopped writing; the tail before the wait, which joins its copy.
	s.releases.push(func() error {
		proc.Kill()
		closeTail()
		var closeErr error
		if sk != nil {
			closeErr = sk.Close()
		}
		return errors.Join(closeErr, encoderResult(s.ctx, proc))
	})
	if err != nil {
		return delivery{}, fmt.Errorf("starting the delivery: %w", err)
	}
	return delivery{sink: sk, output: encoderOutput{proc: proc, ended: sk.Drained()}}, nil
}

// relay serves the read's own spool, the read standing in for an encoder that would rewrite it unchanged.
func (s *pipeline) relay(relay output.Relayer, o deliver.Opening, reader *pull) (delivery, error) {
	sk, err := relay.OpenSpool(s.ctx, o, reader.spool, reader.Done(), reader.Progress)
	if err != nil {
		return delivery{}, fmt.Errorf("starting the delivery: %w", err)
	}
	s.releases.push(func() error {
		closeErr := sk.Close()
		// The read stands in for the encoder, so a read that failed fails the cast as that encoder would have.
		if s.ctx.Err() != nil {
			return closeErr
		}
		return errors.Join(closeErr, reader.Err())
	})
	slog.InfoContext(s.ctx, "serving the read's buffer as it is, since the encode would change nothing")
	return delivery{sink: sk, output: reader}, nil
}

// starved reports whether the device ended on an unfinished read after taking everything it had made, and what that was.
func starved(f feed, d delivery) (made media.Progress, sent int64, ok bool) {
	if f.buffered == nil {
		return made, 0, false
	}
	select {
	case <-f.buffered.reader.Done():
		return made, 0, false
	default:
	}
	streamed, ok := d.sink.(interface{ Handed() (int64, time.Time) })
	if !ok {
		return made, 0, false
	}
	sent, _ = streamed.Handed()
	made = d.output.Progress()
	return made, sent, made.Bytes > 0 && sent >= made.Bytes
}

const (
	// idleGrace is how long a device that stopped asking is waited for before the delivery is done.
	idleGrace = 30 * time.Second

	// writeDeadline outlasts a stall verdict, so the watch judges a quiet device before a write gives up.
	writeDeadline = health.StallWindow + idleGrace
)

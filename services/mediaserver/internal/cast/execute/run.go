// Package execute runs one attempt of a cast over real processes, servers and devices.
package execute

import (
	"context"
	"errors"
	"fmt"
	"github.com/stupside/castor/internal/telemetry"
	"log/slog"
	"net/url"
	"os"

	"github.com/stupside/castor/services/mediaserver/internal/cast/compose"
	"github.com/stupside/castor/services/mediaserver/internal/cast/health"
	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/subtitle"
)

var _ recovery.Runner = Cast{}

// Run casts a end to end; everything it acquired is released before its outcome is read.
func (c Cast) Run(parent context.Context, a recovery.Attempt) (result recovery.Outcome) {
	parent, finish := telemetry.Start(parent, telemetry.Attempt)
	defer func() { finish(result.Err) }()
	if err := a.Program.Validate(); err != nil {
		err = fmt.Errorf("attempt has no valid media program: %w", err)
		return recovery.Outcome{Err: err, Evidence: evidence(ran{}, err, parent.Err() != nil)}
	}
	s := open(parent, c, a)
	r, err := s.play()
	err = errors.Join(err, s.releases.release())
	return recovery.Outcome{Err: err, Evidence: evidence(r, err, parent.Err() != nil)}
}

// pipeline is what one attempt runs on and never changes once opened; every step returns what it established.
type pipeline struct {
	cast    Cast
	attempt recovery.Attempt

	ctx context.Context
	// releases undoes everything the attempt acquired, once it ends.
	releases *releases
	// shown are the captions the device is handed with the stream; the source's own, when it publishes the language asked.
	shown []media.Caption
}

// ran is how far an attempt's pipeline got, and the read it started on the way (nil where it read nothing).
type ran struct {
	reached health.Phase
	reader  *pull
}

func open(parent context.Context, c Cast, a recovery.Attempt) *pipeline {
	ctx, cancel := context.WithCancel(parent)
	rel := &releases{}
	// Released last: whatever the attempt started stops before it is over.
	rel.push(func() error { cancel(); return nil })
	return &pipeline{cast: c, attempt: a, ctx: ctx, releases: rel}
}

func (s *pipeline) play() (ran, error) {
	s.shown = s.shownCaptions()
	switch s.compose() {
	case compose.Handoff:
		return s.handoff()
	case compose.ReadOnce:
		return s.readOnce()
	default:
		return s.remux()
	}
}

func (s *pipeline) handoff() (ran, error) {
	// Present because run validated this program before the composition ran.
	primary, _ := s.attempt.Program.PrimaryInput()
	if err := hand(s.ctx, s.cast.Device, primary.URL, primary.ContentType, s.shown, true); err != nil {
		return ran{}, err
	}
	// A LOAD acknowledgement starts playback; it does not transfer ownership of
	// the cast's resources. Keep captions and the device lease until playback ends.
	return ran{reached: health.Playing}, s.cast.Device.AwaitEnd(s.ctx)
}

// prepare is the work directory and the program with its followed timelines, which a cast castor serves starts from.
func (s *pipeline) prepare() (string, media.Program, error) {
	work, err := os.MkdirTemp("", "castor-")
	if err != nil {
		return "", media.Program{}, fmt.Errorf("creating work directory: %w", err)
	}
	s.releases.push(func() error { return os.RemoveAll(work) })
	program, err := s.follow(s.attempt.Program)
	return work, program, err
}

func (s *pipeline) remux() (ran, error) {
	work, program, err := s.prepare()
	if err != nil {
		return ran{}, err
	}
	reached, err := s.serve(work, feed{program: program}, nil)
	return ran{reached: reached}, err
}

func (s *pipeline) readOnce() (ran, error) {
	work, program, err := s.prepare()
	if err != nil {
		return ran{}, err
	}
	buf, err := s.read(work, program)
	if err != nil {
		return ran{}, err
	}
	reading := ran{reached: health.Reading, reader: buf.reader}
	if err := gate(s.ctx, buf); err != nil {
		return reading, err
	}
	f := feed{buffered: buf}
	var burn burnRenderer
	if _, native := s.nativeOutput(f); buf.transcript != nil && !native {
		burn = subtitle.NewCueFile(work, buf.transcript.Cues())
	}
	reached, err := s.serve(work, f, burn)
	return ran{reached: max(reading.reached, reached), reader: buf.reader}, err
}

func (s *pipeline) compose() compose.Kind {
	shape := compose.Shape{
		Device:    s.playbackCapabilities(),
		Program:   s.attempt.Program,
		Delivery:  s.attempt.Delivery,
		Height:    s.attempt.SelfFetchHeight(),
		MaxHeight: s.cast.MaxHeight,
	}
	kind := compose.For(shape)
	// Requested transcription needs a buffered PCM feed whenever no usable
	// native captions are shown, including sources that publish no caption track.
	if s.cast.Subtitles != nil && len(s.shown) == 0 {
		kind = compose.ReadOnce
	}
	slog.InfoContext(s.ctx, "cast composition", "composition", string(kind))
	return kind
}

// follow points the inputs whose timelines castor keeps at castor's republished playlists.
func (s *pipeline) follow(program media.Program) (media.Program, error) {
	followed, unfollow, err := s.cast.Timelines.Republish(s.ctx, program)
	if err != nil {
		return media.Program{}, &timelineUnreadable{err: err}
	}
	s.releases.push(unfollow)
	return followed, nil
}

// timelineUnreadable is a source whose timeline castor must keep and could not read.
type timelineUnreadable struct{ err error }

func (e *timelineUnreadable) Error() string {
	return "following the source's timeline: " + e.err.Error()
}
func (e *timelineUnreadable) Unwrap() error { return e.err }

// hand points the device at target; source says whether that is the source itself rather than what castor serves.
func hand(ctx context.Context, dev Device, target *url.URL, contentType string, shown []media.Caption, source bool) error {
	slog.InfoContext(ctx, "starting playback", "host", target.Host, "content_type", contentType, "handoff", source)
	if err := dev.Play(ctx, target, contentType, shown); err != nil {
		return &playRefused{err: err, source: source}
	}
	return nil
}

// playRefused is the device's own refusal of the URL it was handed.
type playRefused struct {
	err    error
	source bool
}

func (e *playRefused) Error() string { return "starting playback: " + e.err.Error() }
func (e *playRefused) Unwrap() error { return e.err }

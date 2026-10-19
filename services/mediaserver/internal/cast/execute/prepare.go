package execute

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/compose"
	"github.com/stupside/castor/services/mediaserver/internal/cast/encoding"
	"github.com/stupside/castor/services/mediaserver/internal/cast/health"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output"
	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/subtitle"
)

// preparation builds one presentation within the attempt's context and resource
// scope. It cannot command the device or transition the attempt state machine.
type preparation struct {
	Machinery
	Publisher    output.Publisher
	Timelines    Timelines
	Captions     *Captions
	MaxHeight    media.HeightCap
	capabilities media.Capabilities
	ctx          context.Context
	attempt      recovery.Attempt
	releases     *releases
	failures     chan<- error
	shown        []media.Caption
	subtitles    Subtitles
}

func newPreparation(ctx context.Context, c Cast, a recovery.Attempt, releases *releases, failures chan<- error) *preparation {
	p := &preparation{Machinery: c.Machinery, Publisher: c.Publisher, Timelines: c.Timelines,
		Captions: c.Captions, MaxHeight: c.MaxHeight, ctx: ctx, attempt: a, releases: releases, failures: failures}
	if c.Device != nil {
		p.capabilities = c.Device.Capabilities()
	}
	if c.Transcription != nil {
		p.subtitles = c.Transcription(ctx, a.Rendition.AudioLanguage)
	}
	return p
}

// presentation is the result of preparation, handed to the coordinator only
// after preparation returns. Its resources remain in the attempt's ownership.
type presentation struct {
	captions    []media.Caption
	target      *url.URL
	contentType string
	source      bool
	delivery    *delivery
	feed        feed
	reached     ran
}

func (s *preparation) preparePresentation() (p presentation, err error) {
	if err := s.ctx.Err(); err != nil {
		return p, err
	}
	s.shown = s.shownCaptions()
	p.captions = s.shown
	if err := s.ctx.Err(); err != nil {
		return p, err
	}
	kind := s.compose()
	if kind == compose.Handoff {
		primary, _ := s.attempt.Program.PrimaryInput()
		p.target, p.contentType, p.source = primary.URL, primary.ContentType, true
		return p, nil
	}
	work, program, err := s.prepare()
	if err != nil {
		return p, err
	}
	p.feed = feed{program: program}
	var burn burnRenderer
	if kind == compose.ReadOnce {
		buf, err := s.read(work, program)
		if err != nil {
			return p, err
		}
		p.reached = ran{reached: health.Reading, reader: buf.reader}
		if err := gate(s.ctx, buf); err != nil {
			return p, err
		}
		ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
		err = encoding.ValidateTracks(ctx, s.Probes.File(buf.reader.spool.ProbePath()), buf.expected, buf.reader.Done())
		cancel()
		if err != nil {
			return p, fmt.Errorf("validating buffered read: %w", err)
		}
		p.feed = feed{buffered: buf}
		if _, native := s.nativeOutput(p.feed); buf.transcript != nil && !native {
			burn = subtitle.NewCueFile(work, buf.transcript.Cues())
		}
	}
	opts, err := s.encode(p.feed, burn)
	if err != nil {
		return p, err
	}
	d, err := s.produce(work, p.feed, opts, burn)
	if err != nil {
		return p, err
	}
	p.delivery = &d
	p.reached.reached = health.Opening
	if err := awaitArtifact(s.ctx, d); err != nil {
		return p, err
	}
	if err := s.validateOutput(d, opts); err != nil {
		return p, err
	}
	p.target, p.contentType = d.URL(), opts.Format.ContentType
	return p, nil
}

// prepare is the work directory and the program with its followed timelines, which a cast castor serves starts from.
func (s *preparation) prepare() (string, media.Program, error) {
	work, err := os.MkdirTemp("", "castor-")
	if err != nil {
		return "", media.Program{}, fmt.Errorf("creating work directory: %w", err)
	}
	s.releases.push(func() error { return os.RemoveAll(work) })
	program, err := s.follow(s.attempt.Program)
	return work, program, err
}

func (s *preparation) compose() compose.Kind {
	shape := compose.Shape{
		Device:    s.playbackCapabilities(),
		Program:   s.attempt.Program,
		Delivery:  s.attempt.Delivery,
		Height:    s.attempt.SelfFetchHeight(),
		MaxHeight: s.MaxHeight,
	}
	kind := compose.For(shape)
	// Requested transcription needs a buffered PCM feed whenever no usable
	// native captions are shown, including sources that publish no caption track.
	if s.subtitles != nil && len(s.shown) == 0 {
		kind = compose.ReadOnce
	}
	slog.InfoContext(s.ctx, "cast composition", "composition", string(kind))
	return kind
}

// follow points the inputs whose timelines castor keeps at castor's republished playlists.
func (s *preparation) follow(program media.Program) (media.Program, error) {
	followed, unfollow, err := s.Timelines.Republish(s.ctx, program)
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

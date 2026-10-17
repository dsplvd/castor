// Package health judges whether a cast is working: the rules, and the loop that applies them.
package health

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// Watch polls one subject until a rule ends the wait: nil to proceed, a *Fault to end.
func Watch(ctx context.Context, m Monitor) error {
	now := time.Now()
	t := &tracker{m: m, start: now, grew: now, kept: now}
	tick := time.NewTicker(pollInterval)
	defer tick.Stop()

	for {
		if err := context.Cause(ctx); err != nil {
			return err
		}
		h := t.read()
		r, act, err := judge(m.Phase, h)
		if err != nil {
			return err
		}
		switch act {
		case open:
			slog.InfoContext(ctx, "watch cleared",
				"gate", m.Subject,
				"rule", r.name,
				"waited", time.Since(t.start).Round(time.Millisecond),
			)
			return nil
		case revise, abandon:
			return t.fault(ctx, r, act, h)
		case hold:
			t.hold(ctx, r)
		case release:
			t.release(ctx, r, h)
		default:
			t.report(ctx, r, h)
		}

		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-tick.C:
		}
	}
}

// Fault is a verdict that ended a watch; recovery keys on its Kind.
type Fault struct {
	Kind Kind
	// Revise is a verdict reached before the device held the URL, so the attempt may change.
	Revise bool

	// Subject is what was watched (gate, output, or cast).
	Subject string

	// Vitals is the measurements the verdict was reached on.
	Vitals Vitals

	// Err is the producer's terminal error (so errors.Is still finds cancellation).
	Err error

	// Evidence is what the producer printed (retained even if castor killed it).
	Evidence []string
}

// DeviceStarved is the fault of a device that ended on its own while the read behind it still ran, having taken everything it had made.
func DeviceStarved(subject string, made media.Progress, handed int64) *Fault {
	return &Fault{
		Kind:    Starved,
		Subject: subject,
		Vitals:  Vitals{Landed: made.Bytes, Position: made.Position, handed: handed},
	}
}

func (f *Fault) Error() string {
	if f.Err != nil {
		return fmt.Sprintf("%s: %s: %s", f.Subject, f.Kind, f.Err)
	}
	return fmt.Sprintf("%s: %s", f.Subject, f.Kind)
}

func (f *Fault) Unwrap() error { return f.Err }

// tracker is one Watch's derived state: how long each thing has been true.
type tracker struct {
	m     Monitor
	start time.Time

	// landed is the artifact's size, and grew when the producer last grew.
	landed int64
	grew   time.Time

	// trail is the media position over the last rateSpan, oldest first.
	trail []mark

	// kept is when the rate last kept up with playback.
	kept time.Time

	// samples counts the speeds the producer stated, not the N/A it prints before.
	sample  media.Progress
	samples int

	// pausedAt is when the device was last held, zero while it plays; held is every hold before it.
	pausedAt time.Time
	held     time.Duration
	// unholdable is a device that refused to pause, judged from then on as one that cannot be held.
	unholdable bool
	// refusedResume is a resume the device refused, which is warned of once however often it is retried.
	refusedResume bool

	reported time.Time
}

// read takes one reading of every port.
func (t *tracker) read() Vitals {
	now := time.Now()
	h := Vitals{subtitles: t.m.Lead != nil, holdable: t.m.Transport != nil && !t.unholdable}

	if t.m.Landed != nil {
		h.Landed = t.m.Landed()
	}

	if l := t.m.Lead; l != nil {
		h.lead, h.leadDone = l.LatestEnd(), l.Done()
	}

	if tm := t.m.Telemetry; tm != nil {
		sample := tm.Progress()
		if sample != t.sample && sample.Speed > 0 {
			t.samples++
		}
		t.sample = sample
		h.Position, h.Samples = sample.Position, t.samples
		h.rate = t.pace(now, h.Position)
		if since := now.Sub(t.start); since > 0 {
			h.fill = h.Position.Seconds() / since.Seconds()
		}
	}
	if h.paced() {
		t.kept = now
	}
	h.sinceDeficit = now.Sub(t.kept)

	// Growth is media at a pace: a muxer pads bytes with no media behind them, and a trickle never goes silent.
	grew := h.Landed != t.landed
	if t.m.Telemetry != nil {
		grew = h.rate >= minPace
	}
	if grew {
		t.landed, t.grew = h.Landed, now
	}
	h.sinceGrowth = now.Sub(t.grew)

	if p := t.m.Producer; p != nil {
		select {
		case <-p.Done():
			h.ended = true
			// A terminal error is published with Done, never before.
			if tm := t.m.Telemetry; tm != nil {
				h.failed = tm.Err() != nil
			}
		default:
		}
	}

	if a := t.m.Audience; a != nil {
		handed, last := a.Handed()
		h.handed = handed
		// A device that fetched nothing yet is measured from the watch's start, not from the epoch.
		h.sinceFetch = now.Sub(cmp.Or(last, t.start))
	}
	// Only a device plays; the watch starts as Play returns, which understates the media on hand.
	if t.m.Phase == Playing {
		h.paused = !t.pausedAt.IsZero()
		held := t.held
		if h.paused {
			h.sincePause = now.Sub(t.pausedAt)
			held += h.sincePause
		}
		h.played, h.held = now.Sub(t.start)-held, held
	}

	if t.m.Grace > 0 {
		h.overdue = now.Sub(t.start) > t.m.Grace
	}

	return h
}

// mark is the media position read at one instant.
type mark struct {
	at       time.Time
	position time.Duration
}

// pace records position and reports the media made per second over the trail, zero until it spans two readings.
func (t *tracker) pace(now time.Time, position time.Duration) float64 {
	t.trail = append(t.trail, mark{at: now, position: position})
	for len(t.trail) > 1 && now.Sub(t.trail[1].at) >= rateSpan {
		t.trail = t.trail[1:]
	}
	oldest := t.trail[0]
	if span := now.Sub(oldest.at); span > 0 {
		return float64(position-oldest.position) / float64(span)
	}
	return 0
}

// hold pauses the device; one that will not be held is judged from then on as one that cannot be.
func (t *tracker) hold(ctx context.Context, r rule) {
	if err := t.m.Transport.Pause(ctx); err != nil {
		t.unholdable = true
		slog.WarnContext(ctx, "the device would not pause, so it is left to play out what it has", "gate", t.m.Subject, "rule", r.name, "error", err)
		return
	}
	t.pausedAt = time.Now()
	slog.InfoContext(ctx, "device held", "gate", t.m.Subject, "rule", r.name)
}

// release resumes a held device; a refusal is retried on the next tick, since the rule still holds.
func (t *tracker) release(ctx context.Context, r rule, h Vitals) {
	if err := t.m.Transport.Resume(ctx); err != nil {
		if !t.refusedResume {
			slog.WarnContext(ctx, "the device would not resume", "gate", t.m.Subject, "rule", r.name, "error", err)
		}
		t.refusedResume = true
		return
	}
	t.refusedResume = false
	t.held += time.Since(t.pausedAt)
	t.pausedAt = time.Time{}
	slog.InfoContext(ctx, "device released", "gate", t.m.Subject, "rule", r.name, "held", h.sincePause.Round(time.Millisecond))
}

// fault ends the watch with the producer's own error and evidence; only an abandon is logged, a revise is the recovery's to say.
func (t *tracker) fault(ctx context.Context, r rule, act action, h Vitals) error {
	f := &Fault{
		Kind:    r.kind,
		Revise:  act == revise,
		Subject: t.m.Subject,
		Vitals:  h,
	}
	if r.blames == theProducer && t.m.Producer != nil {
		if tm := t.m.Telemetry; tm != nil {
			f.Err = tm.Err()
		}
		f.Evidence = t.m.Producer.Evidence()
	}
	if act == abandon {
		slog.WarnContext(ctx, "cast abandoned", "gate", t.m.Subject, "verdict", r.kind.String(), "rule", r.name)
	}
	return f
}

// report says what the watch is waiting on.
func (t *tracker) report(ctx context.Context, r rule, h Vitals) {
	if time.Since(t.reported) < reportInterval {
		return
	}
	t.reported = time.Now()
	slog.DebugContext(ctx, "watch",
		"gate", t.m.Subject,
		"rule", r.name,
		"media_position", h.Position.Round(time.Second),
		"ahead", h.ahead().Round(time.Second),
		"paused", h.paused,
	)
}

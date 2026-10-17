// Package follow serves ffmpeg the timelines castor keeps: each followed input republished on loopback, every resource it lists read by castor.
package follow

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/source"
)

// Republisher serves ffmpeg the inputs whose timelines castor keeps, on loopback, for one read.
type Republisher struct {
	client    source.Client
	formats   source.Formats
	repackage Repackage
}

// New follows timelines on client, which must be the session resolution read through; repackage serves fMP4 as MPEG-TS.
func New(client source.Client, formats source.Formats, repackage Repackage) Republisher {
	return Republisher{client: client, formats: formats, repackage: repackage}
}

// Republish points each followed input at its republished timeline; the func stops serving them.
func (r Republisher) Republish(parent context.Context, program media.Program) (media.Program, func() error, error) {
	// Reads ahead of the reader outlive no read: stopping the republish ends them.
	ctx, cancel := context.WithCancel(parent)
	var feeds []*feed
	var owned []*feed
	stop := func() {
		cancel()
		for _, feed := range owned {
			feed.wait()
		}
	}
	followed := map[media.InputID]bool{}
	for _, in := range program.Inputs {
		src := r.formats.Claiming(in.ContentType).Timeline(r.client, in, reads(program, in.ID))
		if src == nil {
			continue
		}
		// Only a timeline that restarts at its seams needs its fragments made MPEG-TS; others are relayed as the origin sent them.
		repackage := r.repackage
		if fetch := in.Fetching(); !fetch.Live && !fetch.Spliced {
			repackage = nil
		}
		feed := newFeed(ctx, string(in.ID), src, repackage)
		owned = append(owned, feed)
		if err := feed.start(); err != nil {
			// A representation has no reading but castor's; a playlist castor cannot follow stays ffmpeg's to read.
			if in.Representation != "" {
				stop()
				return media.Program{}, nil, fmt.Errorf("reading the %s input's presentation: %w", in.ID, err)
			}
			slog.WarnContext(ctx, "castor could not read this input's timeline, so ffmpeg reads the origin directly",
				"input", in.ID, "error", err)
			continue
		}
		feeds, followed[in.ID] = append(feeds, feed), true
	}
	if len(feeds) == 0 {
		stop()
		return program, func() error { return nil }, nil
	}
	server, err := serve(feeds...)
	if err != nil {
		stop()
		return media.Program{}, nil, err
	}
	out := program.Clone()
	for i, in := range out.Inputs {
		if followed[in.ID] {
			// The republished playlist is the representation, so the input names nothing inside it any more.
			out.Inputs[i].URL, out.Inputs[i].ContentType, out.Inputs[i].Representation = server.URL(string(in.ID)), media.HLS, ""
			slog.InfoContext(ctx, "castor follows this input's timeline", "input", in.ID, "origin", in.URL.Redacted(), "republished", out.Inputs[i].URL)
		}
	}
	return out, func() error {
		cancel()
		err := server.Close()
		stop()
		return err
	}, nil
}

// reads is the kind of track a program reads from input: the one it requires, the first it names otherwise.
func reads(program media.Program, input media.InputID) media.TrackKind {
	var named media.TrackKind
	for _, t := range program.Tracks {
		switch {
		case t.Input != input:
		case !t.Optional:
			return t.Kind
		case named == "":
			named = t.Kind
		}
	}
	return named
}

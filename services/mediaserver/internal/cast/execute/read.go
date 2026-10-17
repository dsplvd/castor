package execute

import (
	"context"
	"log/slog"
	"path/filepath"
	"slices"

	"github.com/stupside/castor/services/mediaserver/internal/cast/codec"
	"github.com/stupside/castor/services/mediaserver/internal/cast/transcode"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// buffered is a read into castor's own spool, and the transcription it feeds (nil where it feeds none).
type buffered struct {
	reader *pull
	burn   Burn
	// live is a read of an edge that arrives at 1x, which no pause gets ahead of.
	live bool
}

// read buffers program under its own context, so releasing it stops the read and its transcription before the work dir goes.
func (s *pipeline) read(work string, program media.Program) (*buffered, error) {
	ctx, stop := context.WithCancel(s.ctx)
	source, err := transcode.NewProgramSource(program, s.attempt.Fetch, s.cast.Binary, s.cast.InputArgs)
	if err != nil {
		stop()
		return nil, err
	}
	facts := measure(ctx, "the source this cast buffers", s.cast.Probes.Source(program, source.ProbeInputs()))
	program = aligned(program, facts.probe.InputStarts)
	if source, err = transcode.NewProgramSource(program, s.attempt.Fetch, s.cast.Binary, s.cast.InputArgs); err != nil {
		stop()
		return nil, err
	}

	burn := s.transcription(ctx, work, facts, program)
	floor, err := codec.Floor(ctx, codec.Inputs{
		Probe:     facts.probe,
		Into:      transcode.SpoolFormat,
		Decode:    s.attempt.Decode,
		MaxHeight: s.cast.MaxHeight,
		Spliced:   program.Seamed(),
		Encoders:  s.cast.Encoders,
	})
	if err != nil {
		stop()
		return nil, err
	}
	logRefusals(ctx, floor)

	var pcmRate int
	if burn != nil {
		pcmRate = burn.SampleRate()
	}
	reader, err := startPull(ctx, pullSpec{
		ffmpegPath: s.cast.FFmpegPath,
		source:     source,
		probe:      facts.probe,
		spoolPath:  filepath.Join(work, "spool"+transcode.SpoolFormat.Extension),
		floor:      floor,
		pcmRate:    pcmRate,
	})
	if err != nil {
		stop()
		return nil, err
	}

	transcribed := make(chan struct{})
	go func() {
		defer close(transcribed)
		if burn != nil {
			burn.Run(ctx, reader.pcm)
		}
	}()
	s.releases.push(func() error {
		stop()
		<-reader.Done()
		<-transcribed
		return nil
	})
	return &buffered{reader: reader, burn: burn, live: slices.ContainsFunc(program.Inputs, func(in media.Input) bool { return in.Fetching().Live })}, nil
}

// transcription is the burn-in this read feeds, nil where subtitles are off or nothing shows the source has sound.
func (s *pipeline) transcription(ctx context.Context, work string, facts facts, program media.Program) Burn {
	if s.cast.Subtitles == nil {
		return nil
	}
	if len(s.shown) > 0 {
		slog.InfoContext(ctx, "no transcribed subtitles for this cast: the source's own captions are shown")
		return nil
	}
	if !facts.sounds(program) {
		slog.InfoContext(ctx, "no subtitles for this cast: nothing shows the source carries sound")
		return nil
	}
	return s.cast.Subtitles(ctx, work)
}

package execute

import (
	"context"
	"log/slog"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/codec"
	"github.com/stupside/castor/services/mediaserver/internal/cast/transcode"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// buffered is a read into castor's own spool, and the transcription it feeds (nil where it feeds none).
type buffered struct {
	reader     *pull
	transcript Transcript
	// captionVideoOffset locates the first video sample on the PCM timeline.
	// An unknown clock cannot safely carry separately timed generated captions.
	captionVideoOffset *time.Duration
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

	transcript := s.transcription(ctx, facts, program)
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
	if transcript != nil {
		pcmRate = transcript.SampleRate()
	}
	live := slices.ContainsFunc(program.Inputs, func(in media.Input) bool { return in.Fetching().Live })
	reader, err := startPull(ctx, pullSpec{
		live:       live,
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

	var transcribing sync.WaitGroup
	if transcript != nil {
		transcribing.Go(func() { transcript.Run(ctx, reader.pcm) })
	}
	s.releases.push(func() error {
		stop()
		<-reader.Done()
		transcribing.Wait()
		return reader.spool.Close()
	})
	var videoOffset *time.Duration
	if track, ok := program.Track(media.TrackVideo); ok && facts.probe.VideoStartOffset != nil {
		offset := *facts.probe.VideoStartOffset + program.Offsets[track.Input]
		videoOffset = &offset
	}
	return &buffered{reader: reader, transcript: transcript, captionVideoOffset: videoOffset, live: live}, nil
}

// transcription reads sound once; native subtitles and burn-in consume the same cues.
func (s *pipeline) transcription(ctx context.Context, facts facts, program media.Program) Transcript {
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
	return s.cast.Subtitles(ctx)
}

package execute

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/stupside/castor/services/mediaserver/internal/cast/codec"
	"github.com/stupside/castor/services/mediaserver/internal/cast/container"
	"github.com/stupside/castor/services/mediaserver/internal/cast/deliver"
	"github.com/stupside/castor/services/mediaserver/internal/cast/fetch"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output"
	"github.com/stupside/castor/services/mediaserver/internal/cast/transcode"
	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// encoding keeps the producer instructions bound to the selected publisher.
type encoding struct {
	transcode.EncodeOptions
	delivery output.Selection
}

// encode decides codecs within the envelope accepted by the output adapter.
func (s *pipeline) encode(f feed, burn burnRenderer) (encoding, error) {
	selected, native := s.nativeOutput(f)
	if !native || burn != nil {
		var err error
		selected, err = s.cast.Outputs.Select(s.playbackCapabilities(), nil)
		if err != nil {
			return encoding{}, err
		}
	}
	into := selected.Plan.Format()

	var burnIn string
	if burn != nil {
		var err error
		if burnIn, err = burn.Create(); err != nil {
			return encoding{}, err
		}
	}

	pace := selected.Plan.Pace(burnIn != "")
	facts, input, err := s.input(f, pace)
	if err != nil {
		return encoding{}, err
	}

	decided, err := s.decide(selected.Capabilities, into, facts, burnIn, f.spliced())
	if err != nil {
		return encoding{}, err
	}
	return encoding{delivery: selected,
		Input:  input,
		Format: into,
		Probe:  facts.probe,
		Video:  decided.Video,
		Audio:  decided.Audio}, nil
}

// input is what the encoder reads from f, measured where it reads it: the spool as it grows, or the source itself.
func (s *pipeline) input(f feed, ceiling fetch.Pace) (facts, transcode.EncodeInput, error) {
	if f.buffered != nil {
		measured := measure(s.ctx, "the local buffer this encode reads", s.cast.Probes.File(f.buffered.reader.spool.ProbePath()))
		return measured, transcode.FromPipe(ceiling), nil
	}
	policies := s.attempt.Fetch.Encoding(f.program, ceiling)
	source, err := transcode.NewProgramSource(f.program, policies, s.cast.Binary, s.cast.InputArgs)
	if err != nil {
		return facts{}, transcode.EncodeInput{}, err
	}
	measured := measure(s.ctx, "the source this remux reads", s.cast.Probes.Source(f.program, source.ProbeInputs()))
	if source, err = transcode.NewProgramSource(aligned(f.program, measured.probe.InputStarts), policies, s.cast.Binary, s.cast.InputArgs); err != nil {
		return facts{}, transcode.EncodeInput{}, err
	}
	return measured, transcode.FromSource(source), nil
}

func (s *pipeline) decide(caps media.Capabilities, into container.Format, facts facts, burnIn string, spliced bool) (codec.Plan, error) {
	decided, err := codec.Served(s.ctx, codec.Inputs{
		Caps: caps, Probe: facts.probe, Measured: facts.measured, Into: into, MaxHeight: s.cast.MaxHeight,
		Spliced: spliced,
		// Attempt's evidence: axis a reader already died copying not handed to second process to copy again.
		Decode:   s.attempt.Decode,
		BurnIn:   burnIn,
		Encoders: s.cast.Encoders,
	})
	if err != nil {
		return codec.Plan{}, fmt.Errorf("planning media: %w", err)
	}

	slog.InfoContext(s.ctx, "encode decision",
		"video_codec", decided.Video.Name(),
		"audio_codec", decided.Audio.Name(),
		"source_video_codec", string(facts.probe.VideoCodec),
		"source_audio_codec", string(facts.probe.AudioCodec),
	)
	logRefusals(s.ctx, decided)
	return decided, nil
}

// logRefusals states once why each axis a plan encodes is not copied.
func logRefusals(ctx context.Context, decided codec.Plan) {
	for _, r := range decided.Refusals {
		slog.InfoContext(ctx, "not copied", "reason", string(r))
	}
}

// startEncoder runs opts in dir, reading tail where it reads a pipe (nil where it reads the source).
func (s *pipeline) startEncoder(opts transcode.EncodeOptions, tail io.Reader, step func(media.Progress), dir string) (*ffmpeg.Process, error) {
	cmd, err := transcode.EncodeArgs(opts)
	if err != nil {
		return nil, fmt.Errorf("building encode args: %w", err)
	}

	slog.DebugContext(s.ctx, "encoder ffmpeg command", "path", s.cast.FFmpegPath, "args", cmd.Args)

	proc, err := ffmpeg.Start(s.ctx, s.cast.FFmpegPath, cmd, ffmpeg.Options{Stdin: tail, WorkDir: dir, Progress: step})
	if err != nil {
		return nil, fmt.Errorf("starting transcode: %w", err)
	}
	return proc, nil
}

func encoderResult(ctx context.Context, proc *ffmpeg.Process) error {
	err := cmp.Or(proc.Wait(), proc.SilentFailure())
	if ctx.Err() == nil && errors.Is(err, deliver.ErrSpoolExpired) {
		return fmt.Errorf("encoder input expired: %w", err)
	}
	if err == nil || ctx.Err() != nil || proc.Evidence().ExitStatus < 0 {
		return nil
	}
	if complaints := proc.Evidence().Lines; len(complaints) > 0 {
		return fmt.Errorf("encoder: %w: %s", err, strings.Join(complaints[max(0, len(complaints)-4):], " | "))
	}
	return fmt.Errorf("encoder: %w", err)
}

type encoderOutput struct {
	proc  *ffmpeg.Process
	ended <-chan struct{}
}

func (o encoderOutput) Done() <-chan struct{} { return o.ended }

func (o encoderOutput) Evidence() []string { return o.proc.Evidence().Lines }

func (o encoderOutput) Progress() media.Progress { return o.proc.Progress() }

// Err is nil: the encoder's own failure is read once, when it is released (see encoderResult).
func (encoderOutput) Err() error { return nil }

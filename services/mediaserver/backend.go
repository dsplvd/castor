package mediaserver

import (
	"context"
	"log/slog"
	"time"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/services/mediaserver/internal/captions"
	"github.com/stupside/castor/services/mediaserver/internal/cast"
	"github.com/stupside/castor/services/mediaserver/internal/cast/deliver"
	"github.com/stupside/castor/services/mediaserver/internal/cast/execute"
	"github.com/stupside/castor/services/mediaserver/internal/cast/recovery"
	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/probe"
	"github.com/stupside/castor/services/mediaserver/internal/source"
	"github.com/stupside/castor/services/mediaserver/internal/source/dash"
	"github.com/stupside/castor/services/mediaserver/internal/source/follow"
	"github.com/stupside/castor/services/mediaserver/internal/source/hls"
	"github.com/stupside/castor/services/mediaserver/internal/source/rank"
	"github.com/stupside/castor/services/mediaserver/internal/source/web"
	"github.com/stupside/castor/services/mediaserver/internal/subtitle/whisper"
	"github.com/stupside/castor/services/mediaserver/internal/wire"
)

// formats is every source format castor reads, in the order a source is offered to them.
var formats = source.Formats{hls.Format{}, dash.Format{}}

const (
	ffmpegBinary  = "ffmpeg"
	ffprobeBinary = "ffprobe"
	// fetchTimeout bounds each playlist fetch and each probe.
	fetchTimeout = 30 * time.Second
	// readDeadline is the mid-read stall bound every fetch plan is derived with.
	readDeadline = 30 * time.Second
)

// backend is the machinery the media server casts with, bound to c.
func (c *Config) backend() func(*mediav1.PlaybackSettings) cast.Caster {
	// One origin session for the engine, so every cast and identification share its cookies.
	client := web.New(fetchTimeout)
	e := &engine{
		cfg:    *c,
		client: client,
		machinery: execute.Machinery{
			FFmpegPath: ffmpegBinary,
			Binary:     ffmpeg.Inspect(ffmpegBinary),
			Encoders:   ffmpeg.Encoders(ffmpegBinary),
			Probes:     probe.FFprobe(ffprobeBinary),
			Timelines:  follow.New(client, formats, follow.Repackager(ffmpegBinary)),
			InputArgs:  formats.InputArgs,
		},
	}
	return func(asked *mediav1.PlaybackSettings) cast.Caster {
		return caster{
			Ranker: rank.New(e.cfg.Resolver.Config, media.HeightCap(asked.GetMaxHeight()), func(s *source.Stream) media.Prober {
				return e.machinery.Probes.Link(probe.Link{URL: s.URL, Headers: s.Headers, InputArgs: formats.InputArgs(s.ContentType, 0)}, fetchTimeout)
			}),
			engine: e,
			asked:  asked,
			subs:   e.subtitles(asked.GetSubtitles()),
		}
	}
}

type engine struct {
	cfg       Config
	client    source.Client
	machinery execute.Machinery
}

// subtitles burns in the selected language with the configured whisper model, none when the selection disables them.
func (e *engine) subtitles(selection *castorv1.SubtitleSelection) execute.Subtitles {
	if selection.GetDisabled() != nil {
		return nil
	}
	language := selection.GetLanguage()
	if selection.GetAutoDetect() != nil {
		language = "auto"
	}
	return func(ctx context.Context, workDir string) execute.Burn {
		b, err := whisper.New(ctx, e.cfg.Whisper, language, workDir)
		if err != nil {
			slog.WarnContext(ctx, "whisper init failed; casting without subtitles", "error", err)
			return nil
		}
		return b
	}
}

// captions shows the source's own captions in the selected language, none when the selection disables them.
func (e *engine) captions(selection *castorv1.SubtitleSelection) *execute.Captions {
	if selection.GetDisabled() != nil {
		return nil
	}
	return &execute.Captions{Want: captions.Want{Language: selection.GetLanguage(), Auto: selection.GetAutoDetect() != nil}, Client: e.client}
}

// caster ranks and plays one cast as it was asked.
type caster struct {
	*rank.Ranker
	engine *engine
	asked  *mediav1.PlaybackSettings
	subs   execute.Subtitles
}

// Measure names what a link carries when it says nothing, then measures it.
func (k caster) Measure(ctx context.Context, s *source.Stream) (*source.Stream, error) {
	if s.ContentType == "" {
		s.ContentType = formats.Identify(ctx, k.engine.client, s.URL)
	}
	return k.Ranker.Measure(ctx, s)
}

func (k caster) Play(ctx context.Context, device execute.Device, listeners deliver.Listeners, streams []*source.Stream, turns recovery.Turns) error {
	run := execute.Cast{
		Machinery: k.engine.machinery,
		Device:    device,
		Listeners: listeners,
		Captions:  k.engine.captions(k.asked.GetSubtitles()),
		Subtitles: k.subs,
		MaxHeight: media.HeightCap(k.asked.GetMaxHeight()),
	}
	// The ranked streams, head first; the rest are what recovery switches to.
	return recovery.Cast(ctx, recovery.Intent{
		Candidates: streams,
		Deadline:   readDeadline,
		Delivery:   wire.FromDelivery(k.asked.GetDelivery()),
		Turns:      turns,
	}, run, source.NewResolver(k.engine.client, run.MaxHeight, k.asked.GetAudioLanguage(), formats))
}

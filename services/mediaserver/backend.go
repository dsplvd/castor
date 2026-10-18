package mediaserver

import (
	"context"
	"log/slog"
	"time"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/services/mediaserver/internal/cast"
	"github.com/stupside/castor/services/mediaserver/internal/cast/deliver"
	"github.com/stupside/castor/services/mediaserver/internal/cast/execute"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output/adapters"
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
	shared := engine{
		cfg: *c,
		machinery: execute.Machinery{
			Outputs:    adapters.Default(),
			FFmpegPath: ffmpegBinary,
			Binary:     ffmpeg.Inspect(ffmpegBinary),
			Encoders:   ffmpeg.Encoders(ffmpegBinary),
			Probes:     probe.FFprobe(ffprobeBinary),
			InputArgs:  formats.InputArgs,
		},
	}
	return func(asked *mediav1.PlaybackSettings) cast.Caster {
		// Host capabilities are immutable and shared; origin credentials and
		// timeline readers belong to this cast, including its recovery attempts.
		e := shared
		e.client = web.New(fetchTimeout)
		e.machinery.Timelines = follow.New(e.client, formats, follow.Repackager(ffmpegBinary))
		return caster{
			Ranker: rank.New(e.cfg.Servers.Media.Resolver.Config, media.HeightCap(asked.GetMaxHeight()), func(s *source.Stream) media.Prober {
				return e.machinery.Probes.Link(probe.Link{URL: s.URL, Headers: s.Headers, InputArgs: formats.InputArgs(s.ContentType, 0)}, fetchTimeout)
			}),
			engine: &e,
			asked:  asked,
		}
	}
}

type engine struct {
	cfg       Config
	client    source.Client
	machinery execute.Machinery
}

// subtitles transcribes the selected language with Whisper; delivery chooses a native track or burn-in.
func (e *engine) subtitles(selection *castorv1.SubtitleSelection, audioLanguage string) execute.Subtitles {
	if !selection.GetPreferences().GetTranscribeIfMissing() {
		return nil
	}
	preferred := selection.GetPreferences().GetLanguages()
	language := transcriptLanguage(preferred, audioLanguage)
	if language == "" {
		return nil
	}
	return func(ctx context.Context) execute.Transcript {
		b, err := whisper.New(ctx, e.cfg.Servers.Media.Whisper, language)
		if err != nil {
			slog.WarnContext(ctx, "whisper init failed; casting without subtitles", "error", err)
			return nil
		}
		return b
	}
}

// transcriptLanguage follows the selected audio when its language is declared.
// Without that fact, the first subtitle preference is the explicit best guess.
func transcriptLanguage(preferred []string, audioLanguage string) string {
	if audioLanguage != "" {
		for _, language := range preferred {
			if source.SameLanguage(audioLanguage, language) {
				return language
			}
		}
		return ""
	}
	if len(preferred) > 0 {
		return preferred[0]
	}
	return ""
}

// captions shows the source's own captions in the selected language, none when the selection disables them.
func (e *engine) captions(selection *castorv1.SubtitleSelection) *execute.Captions {
	if selection.GetDisabled() != nil {
		return nil
	}
	return &execute.Captions{Languages: selection.GetPreferences().GetLanguages(), Client: e.client}
}

// caster ranks and plays one cast as it was asked.
type caster struct {
	*rank.Ranker
	engine *engine
	asked  *mediav1.PlaybackSettings
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
		MaxHeight: media.HeightCap(k.asked.GetMaxHeight()),
	}
	// The ranked streams, head first; the rest are what recovery switches to.
	return recovery.Cast(ctx, recovery.Intent{
		Candidates: streams,
		Deadline:   readDeadline,
		Delivery:   wire.FromDelivery(k.asked.GetDelivery()),
		Turns:      turns,
	}, subtitleRunner{Cast: run, engine: k.engine, selection: k.asked.GetSubtitles()}, source.NewResolver(k.engine.client, run.MaxHeight, k.asked.GetAudio().GetLanguages(), formats))
}

// subtitleRunner binds the generated-caption language after source resolution,
// before composition decides whether a buffered PCM read is needed.
type subtitleRunner struct {
	execute.Cast
	engine    *engine
	selection *castorv1.SubtitleSelection
}

func (r subtitleRunner) Run(ctx context.Context, attempt recovery.Attempt) recovery.Outcome {
	r.Cast.Subtitles = r.engine.subtitles(r.selection, attempt.Rendition.AudioLanguage)
	if r.selection.GetPreferences().GetTranscribeIfMissing() && r.Cast.Subtitles == nil {
		slog.WarnContext(ctx, "transcription skipped: selected audio language is not among subtitle preferences",
			"audio_language", attempt.Rendition.AudioLanguage,
			"preferred_languages", r.selection.GetPreferences().GetLanguages())
	}
	return r.Cast.Run(ctx, attempt)
}

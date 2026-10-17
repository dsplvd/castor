package execute

import (
	"context"
	"io"
	"net/url"

	"github.com/stupside/castor/services/mediaserver/internal/cast/codec"
	"github.com/stupside/castor/services/mediaserver/internal/cast/deliver"
	"github.com/stupside/castor/services/mediaserver/internal/cast/health"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output"
	"github.com/stupside/castor/services/mediaserver/internal/cast/transcode"
	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/probe"
	"github.com/stupside/castor/services/mediaserver/internal/subtitle"
)

// Machinery is what every attempt of every cast runs on.
type Machinery struct {
	// Outputs are the delivery adapters installed by the media server.
	Outputs output.Factory

	// FFmpegPath is the binary every read and encode runs, and Binary what it understands.
	FFmpegPath string
	Binary     ffmpeg.Binary

	// Encoders is the host's encoder lookup, bound to that binary.
	Encoders codec.Encoders

	// Probes measures what an attempt reads: its source program, or its local buffer.
	Probes probe.FFprobe

	// Timelines serves the inputs whose timelines castor keeps.
	Timelines Timelines

	// InputArgs is how ffmpeg opens an input, as its source format says.
	InputArgs transcode.InputArgs
}

// Cast is what one cast's attempts run with, on the machinery every cast shares.
type Cast struct {
	Machinery

	Device Device

	Listeners deliver.Listeners

	// Captions is nil for a cast that wants none of the source's own.
	Captions *Captions

	// Subtitles is nil when generated subtitles are disabled.
	Subtitles Subtitles

	// MaxHeight is the tallest picture the cast may show.
	MaxHeight media.HeightCap
}

// Device is what a cast plays on, connected for the whole cast by whoever lent it, who also closes it.
type Device interface {
	// Play points the device at streamURL, advertised as contentType.
	Play(ctx context.Context, streamURL *url.URL, contentType string, captions []media.Caption) error

	AwaitEnd(ctx context.Context) error

	// Pause holds the device on its picture until Resume, while the read behind it gets ahead again.
	health.Transport

	Capabilities() media.Capabilities
}

// Subtitles opens the transcription chosen for this attempt, independently of
// how its cues are rendered. Source and language selection happened upstream.
type Subtitles func(ctx context.Context) Transcript

// Transcript owns recognition and exposes committed timed cues to the selected renderer.
type Transcript interface {
	health.Lead
	SampleRate() int
	Run(ctx context.Context, pcm io.ReadCloser)
	Cues() *subtitle.Cues
}

// burnRenderer draws committed cues into video for receivers without native delivery.
type burnRenderer interface {
	Create() (string, error)
	Writer(ctx context.Context) func(media.Progress)
}

// Timelines republishes a program's followed inputs for one read; the func stops serving them.
type Timelines interface {
	Republish(ctx context.Context, program media.Program) (media.Program, func() error, error)
}

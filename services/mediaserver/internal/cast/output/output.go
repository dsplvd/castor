// Package output defines the delivery port consumed by playback orchestration.
// Protocol adapters own publication, muxer configuration and pacing.
package output

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"slices"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/container"
	"github.com/stupside/castor/services/mediaserver/internal/cast/deliver"
	"github.com/stupside/castor/services/mediaserver/internal/cast/fetch"
	"github.com/stupside/castor/services/mediaserver/internal/cast/health"
	"github.com/stupside/castor/services/mediaserver/internal/media"
	"github.com/stupside/castor/services/mediaserver/internal/subtitle"
)

// Captions supplies timed cues and their measured position on the media clock.
type Captions struct {
	Cues        *subtitle.Cues
	VideoOffset time.Duration
}

// Output owns publication until Close; its producer remains owned by the caller.
type Output interface {
	URL() *url.URL
	Wait(context.Context) error
	Artifact() deliver.Artifact
	Drained() <-chan struct{}
	Audience() health.Audience
	Settled() error
	Close() error
}

// Plan binds a supported presentation to the producer format and its publisher.
// EncoderFormat can differ from Format when an adapter packages producer bytes.
type Plan interface {
	Format() container.Format
	EncoderFormat() container.Format
	Pace(burning bool) fetch.Pace
	Reserve(dir string) (func() error, error)
	Open(context.Context, deliver.Opening, string, io.ReadCloser, func() media.Progress) (Output, error)
}

// Relayer is implemented by outputs that can serve an existing buffer without
// starting another encoder. Other protocols need only implement Plan.
type Relayer interface {
	OpenSpool(context.Context, deliver.Opening, *deliver.Spool, <-chan struct{}, func() media.Progress) (Output, error)
}

// Adapter accepts complete transport envelopes. An unsupported caption request
// is declined rather than silently discarded.
type Adapter interface {
	Select(media.FormatSupport, *Captions) (Plan, bool)
}

type Factory []Adapter

// Selection retains the exact envelopes admitted by the selected adapter.
type Selection struct {
	Plan         Plan
	Capabilities media.Capabilities
}

func (f Factory) Select(caps media.Capabilities, captions *Captions) (Selection, error) {
	for _, envelope := range caps.Formats {
		for _, adapter := range f {
			plan, ok := adapter.Select(envelope, captions)
			if !ok {
				continue
			}
			caps.Formats = slices.DeleteFunc(slices.Clone(caps.Formats), func(other media.FormatSupport) bool { return other.Transport != envelope.Transport })
			return Selection{Plan: plan, Capabilities: caps}, nil
		}
	}
	return Selection{}, fmt.Errorf("no delivery adapter accepts the requested playback envelopes")
}

// Package progressive adapts growing byte streams to the output port.
package progressive

import (
	"context"
	"io"

	"github.com/stupside/castor/services/mediaserver/internal/cast/container"
	"github.com/stupside/castor/services/mediaserver/internal/cast/deliver"
	"github.com/stupside/castor/services/mediaserver/internal/cast/fetch"
	"github.com/stupside/castor/services/mediaserver/internal/cast/health"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

type Adapter struct{}

func (Adapter) Select(f media.FormatSupport, c *output.Captions) (output.Plan, bool) {
	if c != nil {
		return nil, false
	}
	switch f.Transport {
	case media.TransportMP4, media.TransportMPEGTS:
		format, _ := container.For(f.Transport.ContentType())
		return streamPlan{format}, true
	}
	return nil, false
}

type streamPlan struct{ format container.Format }

func (p streamPlan) Format() container.Format        { return p.format }
func (p streamPlan) EncoderFormat() container.Format { return p.format }
func (p streamPlan) Pace(burn bool) fetch.Pace {
	if burn {
		return fetch.Pace{Realtime: 1.15, Burst: fetch.EncodeBurst}
	}
	return fetch.Pace{}
}
func (streamPlan) Reserve(string) (func() error, error) { return nil, nil }
func (p streamPlan) Open(ctx context.Context, o deliver.Opening, dir string, r io.ReadCloser, made func() media.Progress) (output.Output, error) {
	o.Format = p.format
	srv, err := deliver.OpenStream(ctx, o, dir, r)
	if err != nil {
		return nil, err
	}
	return streamOutput{srv, made}, nil
}

func (p streamPlan) OpenSpool(ctx context.Context, o deliver.Opening, sp *deliver.Spool, drained <-chan struct{}, made func() media.Progress) (output.Output, error) {
	o.Format = p.format
	srv, err := deliver.OpenSpooledStream(ctx, o, sp, drained)
	if err != nil {
		return nil, err
	}
	return streamOutput{srv, made}, nil
}

type streamOutput struct {
	*deliver.Stream
	made func() media.Progress
}

func (s streamOutput) Audience() health.Audience { return s }
func (s streamOutput) Settled() error {
	if err := s.Stream.Err(); err != nil {
		return err
	}
	handed, _ := s.Handed()
	return health.Shortfall(handed, s.made())
}

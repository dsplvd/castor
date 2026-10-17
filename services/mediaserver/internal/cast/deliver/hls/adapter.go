// Package hls implements bounded HLS publication and generated subtitle renditions.
package hls

import (
	"context"
	"io"
	"strconv"

	"github.com/stupside/castor/services/mediaserver/internal/cast/container"
	"github.com/stupside/castor/services/mediaserver/internal/cast/deliver"
	"github.com/stupside/castor/services/mediaserver/internal/cast/fetch"
	"github.com/stupside/castor/services/mediaserver/internal/cast/health"
	"github.com/stupside/castor/services/mediaserver/internal/cast/output"
	"github.com/stupside/castor/services/mediaserver/internal/ffmpeg"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

type Adapter struct{}

func (Adapter) Select(f media.FormatSupport, c *output.Captions) (output.Plan, bool) {
	switch f.Transport {
	case media.TransportHLS, media.TransportHLSWebVTT:
		if c != nil && (f.Transport != media.TransportHLSWebVTT || c.Cues == nil || c.VideoOffset < 0 || c.VideoOffset >= CaptionSegmentDuration) {
			return nil, false
		}
		return hlsPlan{captions: c}, true
	}
	return nil, false
}

type hlsPlan struct{ captions *output.Captions }

func format() container.Format {
	return container.Format{
		ContentType: media.HLS, Extension: ".m3u8", Muxer: ffmpeg.FormatHLS, Delivery: container.DeliverSegmented, Framing: media.FramingOutOfBand,
		Tuning: container.Tuning{Args: []string{
			"-hls_time", strconv.FormatFloat(CaptionSegmentDuration.Seconds(), 'f', -1, 64),
			"-hls_list_size", strconv.Itoa(captionWindowSegments),
			"-hls_flags", "delete_segments+independent_segments",
			"-hls_segment_type", "fmp4",
			"-hls_fmp4_init_filename", "init.mp4",
			"-hls_segment_filename", "seg_%05d.m4s",
		}, Output: "stream.m3u8"},
	}
}
func (hlsPlan) Format() container.Format { return format() }
func (p hlsPlan) EncoderFormat() container.Format {
	if p.captions == nil {
		return p.Format()
	}
	f, _ := container.For(media.MP4)
	f.Tuning.MovFlags = []string{"delay_moov", "default_base_moof"}
	micros := strconv.FormatInt(CaptionSegmentDuration.Microseconds(), 10)
	f.Tuning.Args = []string{"-frag_duration", micros, "-min_frag_duration", micros}
	return f
}
func (p hlsPlan) Pace(burn bool) fetch.Pace {
	// Every rolling rendition stays at receiver speed, including burn-in.
	if p.captions != nil || burn {
		return fetch.Pace{Realtime: 1, Burst: fetch.EncodeBurst}
	}
	return fetch.Pace{Realtime: 1, Burst: CaptionSegmentDuration * captionWindowSegments}
}
func (hlsPlan) Reserve(dir string) (func() error, error) { return Reserve(dir) }
func (p hlsPlan) Open(ctx context.Context, o deliver.Opening, dir string, r io.ReadCloser, made func() media.Progress) (output.Output, error) {
	o.Format = p.Format()
	var srv *Segments
	var err error
	if p.captions == nil {
		srv, err = Open(ctx, o, dir, r)
	} else {
		srv, err = OpenCaptions(ctx, o, dir, r, p.captions.Cues, p.captions.VideoOffset)
	}
	if err != nil {
		return nil, err
	}
	return segmentedOutput{srv, made}, nil
}

type segmentedOutput struct {
	*Segments
	made func() media.Progress
}

func (segmentedOutput) Audience() health.Audience { return nil }
func (s segmentedOutput) Settled() error          { return health.NoneFetched(s.Served(), s.made()) }

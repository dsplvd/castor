package encoding

import (
	"context"
	"fmt"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// ValidateOutput checks a growing encoded artifact against receiver capabilities.
// It observes production completion without owning or cancelling the producer.
func ValidateOutput(ctx context.Context, prober media.Prober, caps media.Capabilities, contentType string, expected media.ProbeInfo, done <-chan struct{}) error {
	completed := false
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("validating encoded output: %w", err)
		}
		measured, _, err := prober.Probe(ctx)
		if cause := ctx.Err(); cause != nil {
			return fmt.Errorf("validating encoded output: %w", cause)
		}
		if err == nil && (expected.VideoCodec == "" || measured.VideoCodec != "") && (expected.AudioCodec == "" || measured.AudioCodec != "") {
			// Container headers can identify tracks and dimensions before the first
			// video packet supplies pixel format / codec parameters. A successful
			// ffprobe exit alone does not make that growing artifact complete.
			incomplete := measured.VideoCodec != "" && (measured.VideoBitDepth == 0 ||
				((measured.VideoCodec == media.CodecH264 || measured.VideoCodec == media.CodecHEVC) && (measured.VideoProfile == "" || measured.VideoLevel < 0)))
			switch {
			case incomplete:
				err = fmt.Errorf("encoded artifact does not yet contain video codec parameters")
			case !caps.CanPlayIn(contentType, measured):
				return fmt.Errorf("encoded output does not satisfy receiver capabilities: video=%s profile=%s level=%d audio=%s", measured.VideoCodec, measured.VideoProfile, measured.VideoLevel, measured.AudioCodec)
			default:
				return nil
			}
		}
		if err == nil {
			err = fmt.Errorf("encoded artifact does not yet contain all expected tracks")
		}
		if completed {
			return fmt.Errorf("validating completed encoded output: %w", err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("validating encoded output: %w", ctx.Err())
		case <-done:
			// The probe may have raced the producer's last write. Observe the final
			// artifact once before deciding it is permanently incomplete.
			completed = true
			done = nil
		case <-time.After(100 * time.Millisecond):
		}
	}
}

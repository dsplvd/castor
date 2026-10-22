package ffmpeg

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

func TestACancelledTestEncodeIsNotRemembered(t *testing.T) {
	bin, err := exec.LookPath("true")
	if err != nil {
		t.Skip("no true binary")
	}
	proven := &provenEncoders{ffmpegPath: bin, slots: map[string]*proof{}}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if works, _ := proven.available(cancelled, libx264); works {
		t.Fatal("a cancelled test encode reported the encoder available")
	}
	works, ran := proven.available(t.Context(), libx264)
	if !works {
		t.Error("a cancelled test encode was cached as unavailable")
	}
	if !ran {
		t.Error("the first uncancelled ask did not run the test encode")
	}
	if _, ran := proven.available(t.Context(), libx264); ran {
		t.Error("a proven encoder was tested again")
	}
}

// fakeFFmpeg is an ffmpeg that succeeds unless an argument it was given is in refuse, and counts its runs.
func fakeFFmpeg(t *testing.T, refuse ...string) (path, runs string) {
	t.Helper()
	dir := t.TempDir()
	runs = filepath.Join(dir, "runs")
	script := "#!/bin/sh\necho run >> " + runs + "\n"
	for _, arg := range refuse {
		script += "for a in \"$@\"; do [ \"$a\" = \"" + arg + "\" ] && exit 1; done\n"
	}
	// A clip is a file ffmpeg was asked to write: the last argument.
	script += "for a in \"$@\"; do last=\"$a\"; done\ncase \"$last\" in *.ts) : > \"$last\";; esac\nexit 0\n"
	path = filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, runs
}

func countRuns(t *testing.T, runs string) int {
	t.Helper()
	b, err := os.ReadFile(runs)
	if err != nil {
		return 0
	}
	return strings.Count(string(b), "run")
}

func TestAGPUPipelineIsProvenPerSourceCodecAndOnlyOnce(t *testing.T) {
	// The fake refuses to make an HEVC clip: that codec cannot be proven, the other can.
	path, runs := fakeFFmpeg(t, "libx265")
	proven := &provenEncoders{ffmpegPath: path, slots: map[string]*proof{}}
	e := vaapiEncoder("h264_vaapi", media.CodecH264, vaapiRenderNode)

	got := proven.withGPU(t.Context(), e)
	if !got.DecodesOnGPU(media.CodecH264, 8) {
		t.Errorf("H.264 was not proven: codecs = %v", got.GPU.Codecs)
	}
	if got.DecodesOnGPU(media.CodecHEVC, 8) {
		t.Error("HEVC was proven although its clip could not be made")
	}
	if got.DecodesOnGPU(media.CodecH264, 10) {
		t.Error("10-bit H.264 took the GPU path")
	}
	if e.GPU.Codecs != nil {
		t.Error("proving a pipeline changed the encoder it was asked about")
	}
	// Two clips and two decodes were tried; asking again runs nothing.
	first := countRuns(t, runs)
	proven.withGPU(t.Context(), e)
	if again := countRuns(t, runs); again != first {
		t.Errorf("a proven pipeline was tested again: %d runs became %d", first, again)
	}
}

func TestAGPUPipelineThatFailsLeavesTheCPUPath(t *testing.T) {
	// The GPU scaler is refused for every codec.
	path, _ := fakeFFmpeg(t, "-hwaccel")
	proven := &provenEncoders{ffmpegPath: path, slots: map[string]*proof{}}
	got := proven.withGPU(t.Context(), vaapiEncoder("h264_vaapi", media.CodecH264, vaapiRenderNode))
	if got.DecodesOnGPU(media.CodecH264, 8) || got.DecodesOnGPU(media.CodecHEVC, 8) {
		t.Errorf("a pipeline whose tests failed was trusted: codecs = %v", got.GPU.Codecs)
	}
}

func TestACancelledGPUTestIsNotRemembered(t *testing.T) {
	path, _ := fakeFFmpeg(t)
	proven := &provenEncoders{ffmpegPath: path, slots: map[string]*proof{}}
	e := vaapiEncoder("h264_vaapi", media.CodecH264, vaapiRenderNode)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if proven.withGPU(cancelled, e).DecodesOnGPU(media.CodecH264, 8) {
		t.Fatal("a cancelled test reported the pipeline proven")
	}
	if !proven.withGPU(t.Context(), e).DecodesOnGPU(media.CodecH264, 8) {
		t.Error("a cancelled test was cached as a failure")
	}
}

func TestAnEncoderWithoutAGPUPipelineIsLeftAlone(t *testing.T) {
	path, runs := fakeFFmpeg(t)
	proven := &provenEncoders{ffmpegPath: path, slots: map[string]*proof{}}
	if got := proven.withGPU(t.Context(), libx264); got.GPU != nil {
		t.Errorf("a software encoder gained a GPU pipeline: %+v", got.GPU)
	}
	if n := countRuns(t, runs); n != 0 {
		t.Errorf("a software encoder cost %d test runs", n)
	}
}

func TestTheEncoderLookupHandsOutTheProvenGPUPipeline(t *testing.T) {
	// Only VA-API is left standing among the hardware H.264 encoders, and its GPU pipeline works for H.264 alone.
	path, _ := fakeFFmpeg(t, "h264_videotoolbox", "h264_nvenc", "h264_qsv", "libx265")
	lookup := Encoders(path)
	e, ok := lookup(t.Context(), media.CodecH264)
	if !ok || e.Name != "h264_vaapi" {
		t.Fatalf("lookup = %q, %v, want h264_vaapi", e.Name, ok)
	}
	if !e.DecodesOnGPU(media.CodecH264, 8) {
		t.Errorf("the encoder the lookup returns has no proven GPU decode: %+v", e.GPU)
	}
	if e.DecodesOnGPU(media.CodecHEVC, 8) {
		t.Error("HEVC decode was reported although it could not be proven")
	}
}

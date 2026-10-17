package probe

import (
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestAudioLeadingVideoRetainsNormalizedPictureOffset(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not on PATH")
	}
	file := filepath.Join(t.TempDir(), "leading.mp4")
	cmd := exec.CommandContext(t.Context(), ffmpeg, "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "color=size=64x64:rate=10:duration=2",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=4",
		"-vf", "setpts=PTS+2/TB", "-fps_mode", "passthrough",
		"-c:v", "libx264", "-c:a", "aac", "-output_ts_offset", "7", file)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v\n%s", err, output)
	}
	info, _, err := FFprobe(ffprobe).File(file).Probe(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// AAC priming may shift the common origin by one packet, but may not erase
	// the two-second delay before the selected picture begins.
	if info.VideoStartOffset == nil || *info.VideoStartOffset < 2*time.Second || *info.VideoStartOffset > 2100*time.Millisecond {
		t.Fatalf("picture offset=%v known=%v, want approximately 2s", info.VideoStartOffset, info.VideoStartOffset != nil)
	}
	if info.Start < 6*time.Second {
		t.Fatalf("fixture lost its nonzero origin: %v", info.Start)
	}
}

package playlist

import (
	"strings"
	"testing"
)

func TestCaptionedMasterRetainsItsHLSHeader(t *testing.T) {
	master := "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1000000\nvideo.m3u8\n"
	got := (sourceCaptions{Language: "fr"}).rewrite(master)
	if !strings.HasPrefix(got, "#EXTM3U\n") {
		t.Fatalf("caption fixture must remain an HLS playlist, got %q", got)
	}
	if !strings.Contains(got, `TYPE=SUBTITLES`) || !strings.Contains(got, `SUBTITLES="subs"`) {
		t.Fatalf("caption rendition was not attached to the variant: %q", got)
	}
}

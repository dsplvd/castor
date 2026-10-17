package serving

import (
	"strings"
	"testing"
)

func TestLinesPreserveInterveningByteRangeTags(t *testing.T) {
	const input = "#EXTM3U\n#EXTINF:1,\n#EXT-X-BYTERANGE:20@100\nstream.m4s\n#EXTINF:1,\n#EXT-X-BYTERANGE:20@120\nstream.m4s\n"
	var output strings.Builder
	count := 0
	for line := range Lines(input) {
		output.WriteString(line.Text)
		output.WriteString(line.URI)
		if line.Entry() {
			count++
			if line.URI != "stream.m4s\n" || !strings.Contains(line.Text, "#EXT-X-BYTERANGE") {
				t.Fatalf("entry=%+v", line)
			}
		}
	}
	if count != 2 || output.String() != input {
		t.Fatal("playlist did not round trip")
	}
}

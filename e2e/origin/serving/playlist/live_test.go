package playlist

import (
	"fmt"
	"github.com/stupside/castor/e2e/origin"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLiveRefusesUnpublishedSegments(t *testing.T) {
	const body = "#EXTM3U\n#EXTINF:1,\nseg_000.ts\n#EXTINF:1,\nseg_001.ts\n#EXTINF:1,\nseg_002.ts\n#EXT-X-ENDLIST\n"
	h := (live{Start: 1, Every: time.Hour, End: Endlist}).Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".m3u8") {
			fmt.Fprint(w, body)
		} else {
			fmt.Fprint(w, "media")
		}
	}), origin.Published{SegmentExt: ".ts", Since: time.Now()})
	get := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest("GET", path, nil))
		return r
	}
	if get("/seg_002.ts").Code != 404 {
		t.Fatal("future segment readable before playlist")
	}
	get("/stream.m3u8")
	if get("/seg_000.ts").Code != 200 || get("/seg_001.ts").Code != 404 {
		t.Fatal("publication boundary ignored")
	}
}

func TestLiveSingleFileOnlyExposesPublishedRanges(t *testing.T) {
	const body = "#EXTM3U\n#EXTINF:1,\n#EXT-X-BYTERANGE:20@100\nstream.m4s\n#EXTINF:1,\n#EXT-X-BYTERANGE:20\nstream.m4s\n"
	spans := placed(body)["stream.m4s"]
	live := live{Start: 1}
	for header, want := range map[string]bool{"bytes=0-99": true, "bytes=100-119": true, "bytes=120-139": false, "bytes=100-139": false, "bytes=100-": false, "": false} {
		if got := live.available(spans, 1, header); got != want {
			t.Errorf("%s: %v", header, got)
		}
	}
	if !live.available(spans, 2, "bytes=100-139") {
		t.Fatal("published combined range refused")
	}
	edge := live.edge(body, 1)
	if strings.Count(edge, "#EXT-X-BYTERANGE") != 1 || strings.Count(edge, "stream.m4s") != 1 {
		t.Fatalf("bad live range playlist: %s", edge)
	}
}

func TestLiveWindowMaterializesImplicitByteOffsets(t *testing.T) {
	const body = "#EXTM3U\n#EXT-X-MEDIA-SEQUENCE:0\n#EXTINF:1,\n#EXT-X-BYTERANGE:20@100\nstream.m4s\n#EXTINF:1,\n#EXT-X-BYTERANGE:20\nstream.m4s\n"
	edge := (live{Window: 1}).edge(body, 2)
	if !strings.Contains(edge, "#EXT-X-BYTERANGE:20@120") {
		t.Fatalf("lost offset: %s", edge)
	}
}

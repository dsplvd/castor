package hls

import (
	"net/url"
	"testing"
)

func FuzzMediaPlaylist(f *testing.F) {
	f.Add("#EXTM3U\n#EXT-X-MEDIA-SEQUENCE:10\n#EXTINF:6,\na.ts\n#EXT-X-ENDLIST\n")
	f.Add("#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"k\"\n#EXT-X-MAP:URI=\"init\",BYTERANGE=\"100@0\"\n#EXTINF:NaN,\na.ts\n")
	f.Fuzz(func(t *testing.T, body string) {
		if len(body) > 4096 {
			t.Skip()
		}
		_, _ = scanMedia(body, &url.URL{Scheme: "https", Host: "origin.test", Path: "/m.m3u8"})
	})
}

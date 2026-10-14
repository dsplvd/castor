package extract

import (
	"net/url"
	"testing"
)

func TestContentTypeOfRule048(t *testing.T) {
	for _, tc := range []struct {
		name, rawURL, contentType, want string
	}{
		{"hls ext", "https://h/a/b.m3u8", "", hlsMIME},
		{"dash ext", "https://h/a.mpd", "", dashMIME},
		{"mp4 ext", "https://h/a.mp4", "", mp4MIME},
		{"m4v ext", "https://h/a.m4v", "", mp4MIME},
		{"mkv ext", "https://h/a.mkv", "", "video/x-matroska"},
		{"webm ext", "https://h/a.webm", "", "video/webm"},
		{"avi ext", "https://h/a.avi", "", "video/x-msvideo"},
		{"mov ext", "https://h/a.mov", "", "video/quicktime"},
		{"ts ext", "https://h/a.ts", "", "video/mp2t"},
		{"ext case insensitive", "https://h/A.M3U8", "", hlsMIME},
		{"query ignored", "https://h/a.m3u8?x=a.mp4", "", hlsMIME},
		{"ext wins over mime", "https://h/a.mp4", "application/dash+xml", mp4MIME},
		{"mime apple hls", "https://h/x", "application/vnd.apple.mpegurl", hlsMIME},
		{"mime x-mpegurl upper", "https://h/x", "Application/X-MpegURL", hlsMIME},
		{"mime audio mpegurl", "https://h/x", "audio/mpegurl", hlsMIME},
		{"mime audio x-mpegurl", "https://h/x", "audio/x-mpegurl", hlsMIME},
		{"mime with params", "https://h/x", "application/vnd.apple.mpegurl; charset=utf-8", hlsMIME},
		{"mime dash", "https://h/x", "application/dash+xml", dashMIME},
		{"mime mp4", "https://h/x", "video/mp4", mp4MIME},
		{"mime flv", "https://h/x", "video/x-flv", "video/x-flv"},
		{"mime upper normalized", "https://h/x", "VIDEO/WEBM", "video/webm"},
		{"flv ext unknown", "https://h/a.flv", "", ""},
		{"unknown ext falls to mime", "https://h/a.bin", "video/mp4", mp4MIME},
		{"html", "https://h/x", "text/html", ""},
		{"empty", "https://h/x", "", ""},
		{"malformed params not matched", "https://h/x", "video/mp4;;=", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u, _ := url.Parse(tc.rawURL)
			if got := contentTypeOf(u, tc.contentType); got != tc.want {
				t.Errorf("contentTypeOf(%q, %q) = %q, want %q", tc.rawURL, tc.contentType, got, tc.want)
			}
		})
	}
	if got := contentTypeOf(nil, "video/mp4"); got != mp4MIME {
		t.Errorf("nil url: %q", got)
	}
	if got := contentTypeOf(nil, ""); got != "" {
		t.Errorf("nil url, no type: %q", got)
	}
}

func TestSegmentedRule048(t *testing.T) {
	for typ, want := range map[string]bool{hlsMIME: true, dashMIME: true, mp4MIME: false, "": false} {
		if segmented(typ) != want {
			t.Errorf("segmented(%q) != %v", typ, want)
		}
	}
}

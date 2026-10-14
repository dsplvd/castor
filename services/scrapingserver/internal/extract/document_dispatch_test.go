package extract

import (
	"net/url"
	"strings"
	"testing"
	"time"

	castorv1 "github.com/stupside/castor/gen/castor/v1"
)

func TestInspectDocumentDispatchRule165(t *testing.T) {
	base, _ := url.Parse("https://cdn.example/a/m.m3u8")
	for _, tc := range []struct {
		name, body string
		ladder     castorv1.Ladder
		refs       int
	}{
		{"hls", "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nv.m3u8\n", castorv1.Ladder_LADDER_MULTIVARIANT, 1},
		{"bom and blank lines", "\xef\xbb\xbf\n  #EXTM3U  \r\nseg.ts\n", castorv1.Ladder_LADDER_SOLE, 1},
		{"hls marker not first line", "x\n#EXTM3U\nseg.ts\n", castorv1.Ladder_LADDER_UNSPECIFIED, 0},
		{"dash", "<MPD/>", castorv1.Ladder_LADDER_MULTIVARIANT, 0},
		{"empty", "", castorv1.Ladder_LADDER_UNSPECIFIED, 0},
		{"oversize", "#EXTM3U\n" + strings.Repeat("a", documentSizeLimit), castorv1.Ladder_LADDER_UNSPECIFIED, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := inspectDocument(tc.body, base)
			if d.ladder != tc.ladder || len(d.references) != tc.refs {
				t.Errorf("ladder %v refs %d", d.ladder, len(d.references))
			}
		})
	}
}

func TestInspectHLSRule049(t *testing.T) {
	base, _ := url.Parse("https://cdn.example/a/m.m3u8")
	for _, tc := range []struct {
		name, body string
		ladder     castorv1.Ladder
		runtime    time.Duration
		refs       []string
	}{
		{"iframe playlist is multivariant", "#EXTM3U\n#EXT-X-I-FRAME-STREAM-INF:URI=\"i.m3u8\"\n", castorv1.Ladder_LADDER_MULTIVARIANT, 0, []string{"https://cdn.example/a/i.m3u8"}},
		{"sums extinf", "#EXTM3U\n#EXTINF:1.5,t\na.ts\n#EXTINF:2,\nb.ts\n#EXT-X-ENDLIST\n", castorv1.Ladder_LADDER_SOLE, 3500 * time.Millisecond, []string{"https://cdn.example/a/a.ts", "https://cdn.example/a/b.ts"}},
		{"master with endlist has no runtime", "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nv.m3u8\n#EXT-X-ENDLIST\n", castorv1.Ladder_LADDER_MULTIVARIANT, 0, []string{"https://cdn.example/a/v.m3u8"}},
		{"closed without extinf is zero", "#EXTM3U\n#EXT-X-ENDLIST\n", castorv1.Ladder_LADDER_SOLE, 0, nil},
		{"negative duration poisons", "#EXTM3U\n#EXTINF:-1,\na.ts\n#EXT-X-ENDLIST\n", castorv1.Ladder_LADDER_SOLE, 0, []string{"https://cdn.example/a/a.ts"}},
		{"absolute and non-http refs", "#EXTM3U\nhttps://o.example/x.ts\nftp://o.example/y.ts\n//p.example/z.ts\n", castorv1.Ladder_LADDER_SOLE, 0, []string{"https://o.example/x.ts", "https://p.example/z.ts"}},
		{"key uri attribute", "#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"k.bin\"\n", castorv1.Ladder_LADDER_SOLE, 0, []string{"https://cdn.example/a/k.bin"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := inspectHLS(tc.body, base)
			var refs []string
			for _, r := range d.references {
				refs = append(refs, r.String())
			}
			if d.ladder != tc.ladder || d.runtime != tc.runtime || strings.Join(refs, " ") != strings.Join(tc.refs, " ") {
				t.Errorf("ladder %v runtime %v refs %v", d.ladder, d.runtime, refs)
			}
		})
	}
}

func TestInspectDASHClassificationRule149(t *testing.T) {
	base, _ := url.Parse("https://cdn.example/a/m.mpd")
	for _, tc := range []struct {
		name, body string
		ok         bool
	}{
		{"mpd root", `<MPD><Period/></MPD>`, true},
		{"xml prolog and namespace", `<?xml version="1.0"?><MPD xmlns="urn:mpeg:dash:schema:mpd:2011"/>`, true},
		{"html root", `<html><body/></html>`, false},
		{"two roots", `<MPD/><MPD/>`, false},
		{"unclosed", `<MPD><Period>`, false},
		{"text before root", `hello<MPD/>`, false},
		{"empty", ``, false},
		{"malformed", `<MPD><</MPD>`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := castorv1.Ladder_LADDER_UNSPECIFIED
			if tc.ok {
				want = castorv1.Ladder_LADDER_MULTIVARIANT
			}
			if d := inspectDASH(tc.body, base); d.ladder != want || d.runtime != 0 {
				t.Errorf("ladder %v", d.ladder)
			}
		})
	}
}

func TestDASHTemplatedReferencesAreSkippedRule149(t *testing.T) {
	base, _ := url.Parse("https://cdn.example/a/m.mpd")
	d := inspectDASH(`<MPD><BaseURL>$x$/</BaseURL><SegmentTemplate media="s-$Number$.m4s" index="i.sidx"/></MPD>`, base)
	if len(d.references) != 1 || d.references[0].String() != "https://cdn.example/a/i.sidx" {
		t.Errorf("refs = %v", d.references)
	}
}

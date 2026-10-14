package extract

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/chromedp/cdproto/network"
)

func TestReplayableRule361(t *testing.T) {
	if replayable(nil) != nil {
		t.Error("nil header must stay nil")
	}
	h := http.Header{}
	for k, v := range map[string]string{
		"Range": "bytes=0-", "Accept-Encoding": "gzip", "Host": "h", "Keep-Alive": "5",
		"Transfer-Encoding": "chunked", "Te": "trailers", "Trailer": "x", "Upgrade": "websocket",
		"Content-Length": "9", "If-Range": "a", "If-None-Match": "b", "If-Modified-Since": "c",
		"If-Match": "d", "If-Unmodified-Since": "e", "Proxy-Connection": "keep-alive",
		"Connection": "keep-alive, X-Hop", "X-Hop": "1",
		"Cookie": "sid=fake", "Authorization": "Bearer fake", "User-Agent": "UA", "X-Keep": "yes", "X-Empty": "",
	} {
		h.Set(k, v)
	}
	want := map[string]string{"Cookie": "sid=fake", "Authorization": "Bearer fake", "User-Agent": "UA", "X-Keep": "yes"}
	got := replayable(h)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if h.Get("Range") == "" {
		t.Error("input header was modified")
	}
}

func TestReplayableOriginRule361(t *testing.T) {
	for _, tc := range []struct{ name, origin, referer, want string }{
		{"derived from referer", "", "https://site.example/p/1?x=2", "https://site.example"},
		{"explicit origin kept", "https://o.example", "https://site.example/p", "https://o.example"},
		{"port kept", "", "http://site.example:8080/p", "http://site.example:8080"},
		{"non web referer", "", "ftp://site.example/p", ""},
		{"no referer", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			if tc.origin != "" {
				h.Set("Origin", tc.origin)
			}
			if tc.referer != "" {
				h.Set("Referer", tc.referer)
			}
			if got := replayable(h)["Origin"]; got != tc.want {
				t.Errorf("Origin = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReplayableJoinsValuesRule361(t *testing.T) {
	h := http.Header{"Cookie": {"a=1", "b=2"}, "Accept": {"x", "y"}}
	got := replayable(h)
	if got["Cookie"] != "a=1; b=2" || got["Accept"] != "x, y" {
		t.Errorf("got %v", got)
	}
}

func TestReplayableBoundRule405(t *testing.T) {
	h := http.Header{"Cookie": {"c=1"}, "Authorization": {"Bearer fake"}, "Referer": {"https://r.example/"}}
	for i := range 100 {
		h.Set(fmt.Sprintf("X-A%03d", i), "v")
	}
	got := replayable(h)
	if len(got) != 64 {
		t.Fatalf("len = %d, want 64", len(got))
	}
	for _, k := range []string{"Cookie", "Authorization", "Referer", "Origin"} {
		if got[k] == "" {
			t.Errorf("identity header %s dropped", k)
		}
	}
	if got["X-A000"] == "" || got["X-A059"] == "" || got["X-A060"] != "" {
		t.Errorf("expected sorted fill up to the 64 budget")
	}
}

func TestToHTTPHeaderRule405(t *testing.T) {
	got := toHTTPHeader(network.Headers{
		"cookie": "a=1", ":authority": "h", "X-Num": 5.0, "Bad Name": "v", "X-Bad": "a\nb", "x-ok": "1",
	})
	if len(got) != 2 || got.Get("Cookie") != "a=1" || got.Get("X-Ok") != "1" || got.Get("X-Num") != "" {
		t.Errorf("got %v", got)
	}
	if toHTTPHeader(nil) != nil || toHTTPHeader(network.Headers{":path": "/"}) != nil {
		t.Error("no usable headers must give nil")
	}
}

func TestToHTTPHeaderLimitsRule405(t *testing.T) {
	many := network.Headers{"cookie": "c=1", "AUTHORIZATION": "fake"}
	for i := range 100 {
		many[fmt.Sprintf("x-%03d", i)] = "v"
	}
	got := toHTTPHeader(many)
	if len(got) != 64 || got.Get("Cookie") == "" || got.Get("Authorization") == "" {
		t.Errorf("count: len = %d", len(got))
	}
	// byte budget: priority headers first, then the rest until 8192 bytes
	big := network.Headers{"Cookie": string(make([]byte, 0)) + repeat('a', 8000), "X-Big": repeat('b', 300), "X-Small": "s"}
	got = toHTTPHeader(big)
	if got.Get("Cookie") == "" || got.Get("X-Big") != "" || got.Get("X-Small") != "s" {
		t.Errorf("byte budget: %v", got)
	}
}

func repeat(b byte, n int) string {
	s := make([]byte, n)
	for i := range s {
		s[i] = b
	}
	return string(s)
}

func TestHopHeadersRule405(t *testing.T) {
	if hopHeaders(nil) != nil {
		t.Error("nil hop")
	}
	hop := &requestHop{headers: http.Header{"Cookie": {"old"}, "A": {"1"}}, wireHeaders: http.Header{"Cookie": {"wire"}}}
	got := hopHeaders(hop)
	if got.Get("Cookie") != "wire" || got.Get("A") != "1" || hop.headers.Get("Cookie") != "old" {
		t.Errorf("got %v", got)
	}
}

func TestAssignExtrasRule405(t *testing.T) {
	e1, e2 := http.Header{"N": {"1"}}, http.Header{"N": {"2"}}
	hops := func(flags ...bool) *requestState {
		r := &requestState{extras: []http.Header{e1, e2}}
		for _, expected := range flags {
			r.hops = append(r.hops, &requestHop{extraKnown: true, extraExpected: expected})
		}
		return r
	}
	r := hops(true, true)
	r.assignExtras()
	if r.hops[0].wireHeaders.Get("N") != "1" || r.hops[1].wireHeaders.Get("N") != "2" {
		t.Error("in order assignment")
	}
	r = hops(false, true)
	r.assignExtras()
	if r.hops[0].wireHeaders != nil || r.hops[1].wireHeaders.Get("N") != "1" {
		t.Error("hop without extra info must be skipped")
	}
	r = hops(true, true, true)
	r.assignExtras()
	if r.hops[2].wireHeaders != nil {
		t.Error("more hops than extras")
	}
	r = &requestState{extras: []http.Header{e1}, hops: []*requestHop{{}, {}}}
	r.assignExtras()
	if r.hops[0].wireHeaders.Get("N") != "1" || r.hops[1].wireHeaders != nil {
		t.Error("unknown extra flags count as expected")
	}
}

func TestOriginOfRule361(t *testing.T) {
	for in, want := range map[string]string{
		"https://a.example/p?q=1#f": "https://a.example", "http://a.example:81/x": "http://a.example:81",
		"": "", "/relative": "", "mailto:a@b": "", "ftp://a.example/": "", "://bad": "",
	} {
		if got := originOf(in); got != want {
			t.Errorf("originOf(%q) = %q, want %q", in, got, want)
		}
	}
}

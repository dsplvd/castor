package extract

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// sourcePage serves html at /page, and runs a session on it; any other path is handed to extra.
func sourcePage(t *testing.T, html string, extra http.HandlerFunc) *session {
	t.Helper()
	chrome := os.Getenv("TEST_CASTOR_CHROME")
	if chrome == "" {
		t.Skip("set TEST_CASTOR_CHROME to exercise a real browser")
	}
	ts := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/page" {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, html)
			return
		}
		if extra != nil {
			extra(w, r)
		}
	}))
	ts.Start()
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	t.Cleanup(cancel)
	s, err := newSession(ctx, BrowserConfig{ChromePath: chrome, Headless: true, Timeout: 10 * time.Second}, ts.URL+"/page")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

const (
	// A page like the ones this is for: its own controls are buttons too, and sit above the list of sources.
	controlsAndSources = `<body style="margin:0"><div style="height:1500px">player</div>
<fieldset><legend>CONTROLS</legend><button onclick="window.__clicked='Reload'">Reload Player</button><button>Float Chat</button></fieldset>
<fieldset><legend>AVAILABLE SOURCES</legend>
<button class="stream-source-btn" aria-pressed="false" onclick="window.__clicked='alpha'"><b>1</b> ALPHA</button>
<button class="stream-source-btn" aria-pressed="false" onclick="window.__clicked='beta'"><b>2</b> BETA (EN)</button>
</fieldset></body>`
)

func TestTheSourcesPanelIsFoundByItsHeadingNotItsButtons(t *testing.T) {
	s := sourcePage(t, controlsAndSources, nil)
	first, err := pickSource(s.ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Picked || first.Panel != "AVAILABLE SOURCES" || !strings.Contains(first.Label, "ALPHA") || first.Total != 2 {
		t.Fatalf("first pick = %+v, want ALPHA of the 2 in AVAILABLE SOURCES, and none of the player's controls", first)
	}
	second, _ := pickSource(s.ctx, 1)
	if !second.Picked || !strings.Contains(second.Label, "BETA") {
		t.Errorf("second pick = %+v, want BETA", second)
	}
	if past, _ := pickSource(s.ctx, 2); past.Picked {
		t.Errorf("a pick past the end of the list = %+v, want none", past)
	}
}

func TestThePickedSourceIsWhereTheClickLands(t *testing.T) {
	// The panel is far below the fold: the pick must scroll it into view, and the click must reach that very button.
	s := sourcePage(t, controlsAndSources, nil)
	for skip, want := range []string{"alpha", "beta"} {
		pick, err := pickSource(s.ctx, skip)
		if err != nil || !pick.Picked {
			t.Fatalf("pick %d = %+v, %v", skip, pick, err)
		}
		if err := click(s.ctx, pick.X, pick.Y); err != nil {
			t.Fatal(err)
		}
		var clicked string
		if err := chromedp.Run(s.ctx, chromedp.Evaluate(`window.__clicked || ''`, &clicked)); err != nil {
			t.Fatal(err)
		}
		if clicked != want {
			t.Errorf("clicking pick %d reached %q, want %q", skip, clicked, want)
		}
	}
}

func TestWhatIsNotAStreamIsNeverPicked(t *testing.T) {
	s := sourcePage(t, `<body style="margin:0">
<fieldset><legend>Servers</legend>
<button>Download our app</button>
<button disabled>Server 1</button>
<button aria-disabled="true">Server 2</button>
<a href="https://elsewhere.test/watch" target="_blank">Server 3</a>
<a href="/other-match">Server 4</a>
<a href="/page?s=5">Server 5</a>
<button>Join our Telegram</button>
<button>Server 6</button>
</fieldset></body>`, nil)
	var got []string
	for skip := range 6 {
		pick, err := pickSource(s.ctx, skip)
		if err != nil {
			t.Fatal(err)
		}
		if !pick.Picked {
			break
		}
		got = append(got, pick.Label)
	}
	// An app promotion, a disabled button, a link that opens another tab and a link to another page are all left out.
	if want := []string{"Server 5", "Server 6"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("picked %q, want %q in the page's order", got, want)
	}
}

func TestOnlyAPanelThatIsAListOfSourcesCounts(t *testing.T) {
	for name, html := range map[string]string{
		"a page with no panel":         `<body><button>Play</button></body>`,
		"a site's navigation":          `<body><nav><fieldset><legend>Streams</legend><button>Football</button></fieldset></nav></body>`,
		"a panel for chat":             `<body><fieldset><legend>Chat links</legend><button>Rules</button></fieldset></body>`,
		"a panel with only ads":        `<body><fieldset><legend>Sources</legend><button>Sponsored: Best VPN</button></fieldset></body>`,
		"a heading too long to be one": `<body><fieldset><legend>All the sources and servers and streams and mirrors that we have for you today</legend><button>One</button></fieldset></body>`,
	} {
		t.Run(name, func(t *testing.T) {
			s := sourcePage(t, html, nil)
			if pick, err := pickSource(s.ctx, 0); err != nil || pick.Picked {
				t.Errorf("pick = %+v, %v, want nothing", pick, err)
			}
		})
	}
}

func TestASourceThePageAlreadyChoseIsLeftAlone(t *testing.T) {
	s := sourcePage(t, `<body><fieldset><legend>Sources</legend>
<button aria-pressed="true" onclick="window.__clicked=1">Chosen</button><button>Other</button></fieldset></body>`, nil)
	pick, _ := pickSource(s.ctx, 0)
	if !pick.Picked || !pick.Selected {
		t.Fatalf("pick = %+v, want the pressed source reported as already selected", pick)
	}
	// A running selection must not click it again; that is selectSources's rule, so here it only has to be said.
	var clicked any
	_ = chromedp.Run(s.ctx, chromedp.Evaluate(`window.__clicked`, &clicked))
	if clicked != nil {
		t.Error("picking a source clicked it")
	}
}

// hitCounter is an origin that notes which sources were clicked, and gives out a playlist when asked for one.
type hitCounter struct {
	mu   sync.Mutex
	hits []string
}

func (h *hitCounter) handler(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasPrefix(r.URL.Path, "/hit/"):
		h.mu.Lock()
		h.hits = append(h.hits, strings.TrimPrefix(r.URL.Path, "/hit/"))
		h.mu.Unlock()
	case r.URL.Path == "/live.m3u8":
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		fmt.Fprint(w, "#EXTM3U\n#EXT-X-VERSION:3\n#EXTINF:4,\nseg0.ts\n")
	}
}

func (h *hitCounter) seen() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return strings.Join(h.hits, ",")
}

// sourcesThatPlay is a page whose sources report their click, and of which only those named in plays start a stream.
func sourcesThatPlay(plays ...int) string {
	var b strings.Builder
	b.WriteString(`<body><fieldset><legend>AVAILABLE SOURCES</legend>`)
	for i := 1; i <= 3; i++ {
		start := ""
		for _, p := range plays {
			if p == i {
				start = "fetch('/live.m3u8');"
			}
		}
		fmt.Fprintf(&b, `<button onclick="fetch('/hit/%d');%s">Source %d</button>`, i, start, i)
	}
	b.WriteString(`</fieldset></body>`)
	return b.String()
}

func TestSourcesAreTriedInOrderUntilOneStartsAStream(t *testing.T) {
	origin := &hitCounter{}
	s := sourcePage(t, sourcesThatPlay(2), origin.handler)
	if err := selectSources(s.ctx, s.collector.hasStream, 2*time.Second, time.Second); err != nil {
		t.Fatal(err)
	}
	if got := origin.seen(); got != "1,2" {
		t.Errorf("clicked sources %q, want 1 then 2, and not 3 once 2 had started a stream", got)
	}
	if !s.collector.hasStream() {
		t.Error("no stream was captured from the source that played")
	}
}

func TestTheFirstSourceThatPlaysIsTheOnlyOneClicked(t *testing.T) {
	origin := &hitCounter{}
	s := sourcePage(t, sourcesThatPlay(1, 2), origin.handler)
	if err := selectSources(s.ctx, s.collector.hasStream, 2*time.Second, time.Second); err != nil {
		t.Fatal(err)
	}
	if got := origin.seen(); got != "1" {
		t.Errorf("clicked sources %q, want only the first", got)
	}
}

func TestSourcesThatNeverPlayAreGivenUpOnAfterTheLimit(t *testing.T) {
	origin := &hitCounter{}
	s := sourcePage(t, sourcesThatPlay(), origin.handler)
	if err := selectSources(s.ctx, s.collector.hasStream, time.Second, time.Second); err != nil {
		t.Fatal(err)
	}
	if got := origin.seen(); got != "1,2,3" {
		t.Errorf("clicked sources %q, want each of the %d allowed, once", got, maxSourceAttempts)
	}
	if s.collector.hasStream() {
		t.Error("a stream was captured from sources that fetch none")
	}
}

func TestAPageWithNoSourcesIsLeftForTheOtherActions(t *testing.T) {
	s := sourcePage(t, `<body><button onclick="fetch('/hit/x')">Play</button></body>`, nil)
	start := time.Now()
	if err := selectSources(s.ctx, s.collector.hasStream, 10*time.Second, time.Second); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("a page with no sources cost %s, want it to cost no more than the wait for them", elapsed)
	}
}

func TestSourcesThatTheActualPageListsLateAreStillFound(t *testing.T) {
	origin := &hitCounter{}
	// The list is filled in a moment after load, as a page does once its own script has fetched it.
	s := sourcePage(t, `<body><div id="slot"></div><script>setTimeout(() => {
	  document.getElementById('slot').innerHTML = '<fieldset><legend>AVAILABLE SOURCES</legend><button onclick="fetch(\'/hit/late\');fetch(\'/live.m3u8\')">Late source</button></fieldset>';
	}, 1500)</script></body>`, origin.handler)
	if err := selectSources(s.ctx, s.collector.hasStream, 2*time.Second, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if got := origin.seen(); got != "late" || !s.collector.hasStream() {
		t.Errorf("clicked %q, stream captured = %v, want the late source clicked and its stream captured", got, s.collector.hasStream())
	}
}

func TestAPageThatIsAlreadyPlayingIsNotWaitedOn(t *testing.T) {
	s := sourcePage(t, `<body><script>fetch('/live.m3u8')</script></body>`, (&hitCounter{}).handler)
	waitUntil(s.ctx, s.collector.hasStream, 3*time.Second)
	start := time.Now()
	if err := selectSources(s.ctx, s.collector.hasStream, time.Second, 30*time.Second); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("a page that already streams cost %s, want it to cost nothing", elapsed)
	}
}

func TestAScriptThatBouncesADevToolsBrowserIsNeverLoaded(t *testing.T) {
	// What the real page does: a library that sends the browser away when it takes it for DevTools.
	origin := func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "disable-devtool") {
			w.Header().Set("Content-Type", "text/javascript")
			fmt.Fprint(w, `window.__bounced = true; location.href = 'about:blank';`)
		}
	}
	s := sourcePage(t, `<body><p id="here">player page</p><script src="/npm/disable-devtool@latest/disable-devtool.min.js"></script></body>`, origin)
	time.Sleep(time.Second)
	var state struct {
		Bounced bool   `json:"bounced"`
		Here    string `json:"here"`
	}
	if err := chromedp.Run(s.ctx, chromedp.Evaluate(`({bounced: !!window.__bounced, here: (document.getElementById('here') || {}).textContent || ''})`, &state)); err != nil {
		t.Fatal(err)
	}
	if state.Bounced || state.Here != "player page" {
		t.Errorf("state = %+v, want the detector refused and the page left where it was", state)
	}
}

func TestTheDetectorPatternsAreUsableURLPatterns(t *testing.T) {
	for _, p := range blockedScripts() {
		if !p.Block || !strings.HasPrefix(p.URLPattern, "*://*:*/*") {
			t.Errorf("pattern %+v must block, and match any scheme, host and port", p)
		}
	}
	if len(blockedScripts()) != len(devtoolsDetectors) {
		t.Errorf("%d patterns for %d detectors", len(blockedScripts()), len(devtoolsDetectors))
	}
}

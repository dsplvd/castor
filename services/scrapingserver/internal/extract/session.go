package extract

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	"github.com/stupside/castor/services/scrapingserver/internal/extract/stealth"
)

// session owns the browser that extracts one page.
type session struct {
	ctx         context.Context
	cancel      context.CancelFunc
	allocCancel context.CancelFunc
	collector   *collector
	centerX     float64
	centerY     float64
}

func newSession(ctx context.Context, cfg BrowserConfig, targetURL string) (*session, error) {
	profile := stealth.New()
	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, append(allocatorOpts(cfg), profile.Flags()...)...)
	taskCtx, taskCancel := chromedp.NewContext(allocCtx)
	width, height := profile.Screen()
	s := &session{
		ctx:         taskCtx,
		cancel:      taskCancel,
		allocCancel: allocCancel,
		centerX:     float64(width) / 2,
		centerY:     float64(height) / 2,
	}
	s.collector = newCollector(taskCtx, s.readBody, graceAfterActions, collectionWindow, preRollWindow)
	chromedp.ListenTarget(s.collector.events, s.collector.listen)

	// No deadline here: the first Run starts the browser, and its context is the browser's lifetime.
	if err := chromedp.Run(taskCtx,
		runtime.Enable(),
		network.Enable(),
		network.SetBlockedURLs().WithURLPatterns(blockedScripts()),
		browser.SetDownloadBehavior(browser.SetDownloadBehaviorBehaviorDeny),
		profile.Apply(),
	); err != nil {
		s.Close()
		return nil, fmt.Errorf("starting the browser: %w", err)
	}

	navCtx, navCancel := context.WithTimeout(taskCtx, cfg.Timeout)
	err := chromedp.Run(navCtx, chromedp.Navigate(targetURL))
	navCancel()
	if err != nil {
		if !s.collector.hasHits() {
			s.Close()
			return nil, fmt.Errorf("navigating to %s: %w", targetURL, err)
		}
		slog.WarnContext(ctx, "navigation did not finish; carrying on with what the page already fetched",
			"url", targetURL, "timeout", cfg.Timeout, "error", err)
	}

	return s, nil
}

// allocatorOpts are the flags a browser castor drives runs with.
func allocatorOpts(cfg BrowserConfig) []chromedp.ExecAllocatorOption {
	opts := []chromedp.ExecAllocatorOption{
		chromedp.ExecPath(cfg.ChromePath),
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
		chromedp.Flag("no-sandbox", cfg.NoSandbox),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-background-timer-throttling", true),
		chromedp.Flag("disable-backgrounding-occluded-windows", true),
		chromedp.Flag("disable-renderer-backgrounding", true),
		chromedp.Flag("autoplay-policy", "no-user-gesture-required"),
		chromedp.Flag("incognito", true),
		// A player embedded from another site is a frame in its own process by default, and what such a frame fetches
		// never reaches the listener on the page. In one process, its streams are seen where they are played.
		chromedp.Flag("disable-features", "IsolateOrigins,site-per-process"),
		chromedp.Flag("disable-site-isolation-trials", true),
	}
	// Only appended when wanted: its presence matters, not its value.
	if cfg.Headless {
		opts = append(opts, chromedp.Flag("headless", "new"))
	}
	return opts
}

// readBody asks this session's page for the bytes of a response it already holds.
func (s *session) readBody(reqID network.RequestID) ([]byte, error) {
	target := chromedp.FromContext(s.ctx).Target
	if target == nil {
		return nil, errors.New("no CDP target on the session")
	}
	return network.GetResponseBody(reqID).Do(cdp.WithExecutor(s.ctx, target))
}

//go:embed js/clear_overlays.js
var clearOverlaysJS string

// clearOverlays disables the invisible layers a page stacks over its player, which would otherwise take the click.
func clearOverlays(ctx context.Context) (int, error) {
	var cleared int
	err := chromedp.Run(ctx, chromedp.Evaluate(clearOverlaysJS, &cleared))
	return cleared, err
}

func click(ctx context.Context, x, y float64) error {
	// Best effort: a page that refuses the script is still clicked.
	if cleared, err := clearOverlays(ctx); err != nil {
		slog.DebugContext(ctx, "clearing overlays failed", "error", err)
	} else if cleared > 0 {
		slog.DebugContext(ctx, "disabled overlays over the player", "count", cleared)
	}
	return chromedp.Run(ctx, chromedp.MouseClickXY(x, y, chromedp.ButtonLeft))
}

//go:embed js/player_pos.js
var playerPosJS string

func clickPlayer(ctx context.Context) error {
	var pos *struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
	}
	if err := chromedp.Run(ctx, chromedp.Evaluate(playerPosJS, &pos)); err != nil || pos == nil {
		return err
	}
	return click(ctx, pos.X, pos.Y)
}

// action is one best-effort nudge at the page: a name for the log and the work.
type action struct {
	name string
	do   func() error
}

// runActions drives the page until it has been driven as far as it can usefully be driven.
func (s *session) runActions() {
	actions := []action{
		{"select source", func() error { return selectSources(s.ctx, s.collector.hasStream, sourceSettle, sourcePanelWait) }},
		{"click player", func() error { return clickPlayer(s.ctx) }},
		{"click", func() error { return click(s.ctx, s.centerX, s.centerY) }},
		{"navigate iframe", func() error {
			return navigateIframe(s.ctx, s.collector.hasMaster)
		}},
		{"bypass turnstile", func() error {
			return bypassTurnstile(s.ctx)
		}},
		{"click", func() error { return click(s.ctx, s.centerX, s.centerY) }},
		{"click player", func() error { return clickPlayer(s.ctx) }},
	}

	for _, a := range actions {
		if s.ctx.Err() != nil {
			return
		}
		// A master playlist is what the page was driven for, so nothing further is asked of it.
		if s.collector.hasMaster() {
			return
		}
		if err := a.do(); err != nil {
			slog.DebugContext(s.ctx, a.name+" failed", "error", err)
		}
	}
}

func (s *session) Close() {
	s.cancel()
	s.collector.close()
	s.allocCancel()
}

//go:embed js/select_source.js
var selectSourceJS string

// sourcePick is what the page's list of sources offered: how many usable ones it holds, and where the asked-for one is.
type sourcePick struct {
	Total    int     `json:"total"`
	Picked   bool    `json:"picked"`
	Label    string  `json:"label"`
	Panel    string  `json:"panel"`
	Selected bool    `json:"selected"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
}

// pickSource asks the page for the skip-th usable source of its list, scrolled into view. A page with no such list answers Picked false.
func pickSource(ctx context.Context, skip int) (sourcePick, error) {
	var pick sourcePick
	err := chromedp.Run(ctx, chromedp.Evaluate("("+selectSourceJS+")("+strconv.Itoa(skip)+")", &pick))
	return pick, err
}

// waitUntil polls done until it holds, d passes or ctx ends, and reports whether it held.
func waitUntil(ctx context.Context, done func() bool, d time.Duration) bool {
	deadline := time.NewTimer(d)
	defer deadline.Stop()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		if done() {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return done()
		case <-tick.C:
		}
	}
}

// awaitSources is the first pick, once the page has had up to wait to list its sources. A page fills its list in after it has
// loaded, so an empty page a moment in is not a page without sources; one that is already playing needs no source at all.
func awaitSources(ctx context.Context, started func() bool, wait time.Duration) (sourcePick, error) {
	var pick sourcePick
	var err error
	waitUntil(ctx, func() bool {
		pick, err = pickSource(ctx, 0)
		return err != nil || pick.Picked || started()
	}, wait)
	return pick, err
}

// selectSources works down the page's own list of sources, in its order, until one makes the player fetch a stream;
// each gets settle to start. Anything the page does not list is left to the rest of the actions.
// Many players sit idle until a source is chosen, so without this nothing is ever requested.
func selectSources(ctx context.Context, started func() bool, settle, panelWait time.Duration) error {
	for tried := range maxSourceAttempts {
		var pick sourcePick
		var err error
		if tried == 0 {
			pick, err = awaitSources(ctx, started, panelWait)
		} else {
			pick, err = pickSource(ctx, tried)
		}
		if err != nil {
			return err
		}
		if !pick.Picked {
			if tried == 0 {
				slog.DebugContext(ctx, "the page lists no sources", "usable", pick.Total)
			} else {
				slog.DebugContext(ctx, "no more sources to try", "tried", tried, "usable", pick.Total)
			}
			return nil
		}
		slog.DebugContext(ctx, "selecting source", "label", pick.Label, "panel", pick.Panel, "source", tried+1, "of", pick.Total, "already_selected", pick.Selected)
		// The page's own choice is left as it is; clicking it again would only restart it.
		if !pick.Selected {
			if err := click(ctx, pick.X, pick.Y); err != nil {
				return err
			}
		}
		if waitUntil(ctx, started, settle/2) {
			return nil
		}
		// A player that loaded idle wants a click of its own.
		if err := clickPlayer(ctx); err != nil {
			slog.DebugContext(ctx, "player click after choosing a source failed", "error", err)
		}
		if waitUntil(ctx, started, settle/2) {
			return nil
		}
		slog.DebugContext(ctx, "the source fetched no stream, trying the next", "label", pick.Label)
	}
	return nil
}

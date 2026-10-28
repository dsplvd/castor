package extract

import (
	"context"
	_ "embed"
	"log/slog"
	"slices"
	"strconv"
	"time"

	"github.com/chromedp/chromedp"
)

//go:embed js/player_frame.js
var playerFrameJS string

// playerFrame is an iframe that looks like the page's player, and where a click in it lands.
type playerFrame struct {
	Src    string  `json:"src"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
	Hint   bool    `json:"hint"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
}

// The smallest a frame without a player's permissions can be and still be taken for a player; a banner is smaller.
const (
	playerLikeWidth  = 400
	playerLikeHeight = 225
)

// playerFrames lists the page's frames that look like a player, largest first: visible, at least 200x120, not a chat or social
// widget, and either embedded with a player's permissions (autoplay, encrypted-media, fullscreen) or as big as a player is.
// With scroll, each is first brought into view.
func playerFrames(ctx context.Context, scroll bool) ([]playerFrame, error) {
	var found []playerFrame
	if err := chromedp.Run(ctx, chromedp.Evaluate("("+playerFrameJS+")("+strconv.FormatBool(scroll)+")", &found)); err != nil {
		return nil, err
	}
	found = slices.DeleteFunc(found, func(f playerFrame) bool {
		return chatFrame(f.Src) || (!f.Hint && (f.Width < playerLikeWidth || f.Height < playerLikeHeight))
	})
	slices.SortStableFunc(found, func(a, b playerFrame) int {
		return int(b.Width*b.Height - a.Width*a.Height)
	})
	return found, nil
}

// clickPlayerFrame clicks the middle of the page's player frame where it sits, for a player that starts on a click and that
// refuses to run once it is the page itself. A stream gets settle, in all, to show up; the click is made again halfway, since the
// first may only have opened an advertisement or unmuted the player. It reports no error for a page with no such frame.
func clickPlayerFrame(ctx context.Context, started func() bool, settle time.Duration) error {
	if started() {
		return nil
	}
	frames, err := playerFrames(ctx, true)
	if err != nil || len(frames) == 0 {
		return err
	}
	f := frames[0]
	for attempt := 1; attempt <= 2; attempt++ {
		slog.DebugContext(ctx, "clicking the player frame in place", "src", f.Src, "attempt", attempt)
		if err := click(ctx, f.X, f.Y); err != nil {
			return err
		}
		if waitUntil(ctx, started, settle/2) {
			return nil
		}
	}
	slog.DebugContext(ctx, "the player frame fetched no stream when clicked", "src", f.Src)
	return nil
}

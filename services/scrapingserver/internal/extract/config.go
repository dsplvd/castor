package extract

import "time"

// Config is everything an Extractor needs.
type Config struct {
	Browser BrowserConfig
	Capture CaptureConfig
}

type BrowserConfig struct {
	Timeout    time.Duration `yaml:"timeout" validate:"required,gt=0"`
	Headless   bool          `yaml:"headless"`
	NoSandbox  bool          `yaml:"no_sandbox"`
	ChromePath string        `yaml:"chrome_path"`
}

type CaptureConfig struct {
	// MaxConcurrency bounds how many page URLs are extracted at once, one browser each.
	MaxConcurrency int `yaml:"max_concurrency" validate:"required,min=1"`
}

// Tuning castor ships: how long a page is given, and how far it is driven.
const (
	maxCaptures            = 100
	collectionWindow       = 10 * time.Second
	graceAfterActions      = 15 * time.Second
	preRollWindow          = 60 * time.Second
	navigateIframeTimeout  = 10 * time.Second
	navigateIframeMaxDepth = 5
	turnstileRetryTimeout  = 10 * time.Second
	bypassTurnstileTimeout = 20 * time.Second
	pageBudget             = 2 * time.Minute
	// maxSourceAttempts is how many of a page's sources are tried before the rest of the page is driven, and sourceSettle how long each gets to start.
	maxSourceAttempts = 3
	sourceSettle      = 12 * time.Second
	// sourcePanelWait is how long a page gets to list its sources: they are filled in after it loads.
	sourcePanelWait = 8 * time.Second
	// sourcePanelWaitWithPlayer is what is left of that wait once a player frame is on the page: a page that already embeds its player
	// has little reason to list sources later, but a list that is built beside it appears within a moment.
	sourcePanelWaitWithPlayer = 2 * time.Second
	// frameClickSettle is how long a player frame gets, in all, to fetch a stream after being clicked in place; the click is repeated halfway,
	// for a player whose first click is taken by an ad or only unmutes it.
	frameClickSettle = 10 * time.Second
)

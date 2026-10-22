package extract

import "github.com/chromedp/cdproto/network"

// devtoolsDetectors are scripts that send a browser away (usually to google.com) when they think DevTools is open.
// A browser castor drives holds a DevTools protocol session, which they take for a person inspecting the page,
// so the player never loads. The page works without them, so their requests are refused.
var devtoolsDetectors = []string{
	"disable-devtool",
	"devtools-detector",
	"devtools-detect",
	"console-ban",
}

// blockedScripts is devtoolsDetectors as the URL patterns Chrome blocks by: any scheme, host and port, with the name in the path.
func blockedScripts() []*network.BlockPattern {
	patterns := make([]*network.BlockPattern, len(devtoolsDetectors))
	for i, name := range devtoolsDetectors {
		patterns[i] = &network.BlockPattern{URLPattern: "*://*:*/*" + name + "*", Block: true}
	}
	return patterns
}

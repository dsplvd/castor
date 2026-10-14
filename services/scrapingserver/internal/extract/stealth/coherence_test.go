package stealth

import (
	"slices"
	"strings"
	"testing"
)

func TestProfilesAreCoherentRule367(t *testing.T) {
	seen := map[string]bool{}
	for range 500 {
		p := New()
		i := slices.IndexFunc(platformPresets, func(pp platformPreset) bool {
			return pp.uaOS == p.uaOS && pp.navigatorPlatform == p.navigatorPlatform && pp.chPlatform == p.platform &&
				pp.chPlatformVersion == p.platformVersion && pp.architecture == p.architecture && pp.bitness == p.bitness
		})
		if i < 0 {
			t.Fatalf("platform fields come from no single preset: %+v", p)
		}
		if !slices.Contains(platformPresets[i].webGLRenderers, webGLPreset{p.webGLVendor, p.webGLRenderer}) {
			t.Fatalf("GPU %q / %q does not belong to %s", p.webGLVendor, p.webGLRenderer, p.platform)
		}
		if p.platform == "Windows" && !strings.Contains(p.webGLRenderer, "Direct3D11") || p.platform == "macOS" && !strings.Contains(p.webGLRenderer, "OpenGL") {
			t.Fatalf("renderer %q does not fit %s", p.webGLRenderer, p.platform)
		}
		if !slices.ContainsFunc(localePresets, func(l localePreset) bool {
			return l.timezoneID == p.timezoneID && l.acceptLanguage == p.acceptLanguage && slices.Equal(l.languages, p.languages)
		}) {
			t.Fatalf("locale mixed: %s %s %v", p.timezoneID, p.acceptLanguage, p.languages)
		}
		if !strings.HasPrefix(p.acceptLanguage, p.languages[0]+",") {
			t.Fatalf("accept-language %q vs languages %v", p.acceptLanguage, p.languages)
		}
		w, h := p.Screen()
		if !slices.Contains(screenPresets, screenPreset{w, h}) {
			t.Fatalf("screen %dx%d", w, h)
		}
		if !slices.Contains(hardwareConcurrencies, p.hardwareConcurrency) || !slices.Contains(deviceMemories, p.deviceMemory) || !slices.Contains(greaseBrands, p.grease) {
			t.Fatalf("hardware/grease out of presets: %+v", p)
		}
		if p.userAgent != "" || p.brands != nil {
			t.Fatalf("version fields must stay empty until identify: %+v", p)
		}
		seen[p.platform+"|"+p.webGLRenderer] = true
	}
	if len(seen) < 4 {
		t.Errorf("500 draws produced only %d platform/GPU combinations", len(seen))
	}
}

func TestNoiseParametersStayInRangeRule073(t *testing.T) {
	seeds := map[uint32]bool{}
	for range 1000 {
		p := New()
		if p.fontNoisePx < 0.001 || p.fontNoisePx >= 0.099 || p.rectNoisePx < 0.001 || p.rectNoisePx >= 0.099 {
			t.Fatalf("px noise out of range: %v %v", p.fontNoisePx, p.rectNoisePx)
		}
		if p.audioNoiseMag < 0.00001 || p.audioNoiseMag >= 0.0001 {
			t.Fatalf("audio noise out of range: %v", p.audioNoiseMag)
		}
		seeds[p.noiseSeed] = true
	}
	if len(seeds) < 990 {
		t.Errorf("noise seed repeats too often: %d distinct of 1000", len(seeds))
	}
}

func TestIdentifyPinsClientHintsRule151(t *testing.T) {
	for _, tc := range []struct{ product, major, full string }{
		{"Chrome/139.0.7258.66", "139", "139.0.7258.66"},
		{"HeadlessChrome/140.1", "140", "140.1"},
		{"Chrome/7", "7", "7"},
	} {
		for range 20 {
			p := New()
			if err := p.identify(tc.product); err != nil {
				t.Fatalf("%s: %v", tc.product, err)
			}
			if !strings.HasPrefix(p.userAgent, "Mozilla/5.0 ("+p.uaOS+") AppleWebKit/537.36 (KHTML, like Gecko) Chrome/"+tc.major+".0.0.0 Safari/537.36") {
				t.Fatalf("ua %q", p.userAgent)
			}
			wantBrands := [][2]string{{p.grease, "8"}, {"Chromium", tc.major}, {"Google Chrome", tc.major}}
			wantFull := [][2]string{{p.grease, "8.0.0.0"}, {"Chromium", tc.full}, {"Google Chrome", tc.full}}
			if !slices.Equal(p.brands, wantBrands) || !slices.Equal(p.fullVersionList, wantFull) {
				t.Fatalf("hints %v / %v", p.brands, p.fullVersionList)
			}
		}
	}
}

func TestIdentifyRejectsUnversionedProductsRule151(t *testing.T) {
	for _, product := range []string{"", "Chrome", "Chrome/", "Chrome/beta", "Chrome/.1", "Chrome/x.1"} {
		p := New()
		if err := p.identify(product); err == nil {
			t.Errorf("identify(%q) accepted", product)
		}
		if p.userAgent != "" || p.brands != nil {
			t.Errorf("identify(%q) changed the profile on error", product)
		}
	}
}

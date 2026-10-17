package source

import "testing"

func TestSameLanguageUnderstandsContainerLanguageCodes(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"fra", "fr", true},
		{"fre", "fr-FR", true},
		{"eng", "en-US", true},
		{"deu", "fr", false},
		{"", "en", false},
	} {
		if got := SameLanguage(tc.a, tc.b); got != tc.want {
			t.Errorf("SameLanguage(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

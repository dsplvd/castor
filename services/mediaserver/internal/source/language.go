package source

import "strings"

// SameLanguage compares two language codes on their primary subtag, so en matches en-US; an empty code matches nothing.
func SameLanguage(a, b string) bool {
	primary := func(s string) string {
		head, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(s)), "-")
		return head
	}
	return primary(a) != "" && primary(a) == primary(b)
}

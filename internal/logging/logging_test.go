package logging

import (
	"bytes"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"testing"
)

func TestRedactionPrecedesEveryDestination(t *testing.T) {
	var a, b bytes.Buffer
	logger := slog.New(Redact(slog.NewMultiHandler(slog.NewJSONHandler(&a, nil), slog.NewJSONHandler(&b, nil))))
	logger.With("api_token", "token-secret").WithGroup("request").Info("fetch https://user:pass@host.test/video?signature=url-secret#fragment",
		"error", errors.New("retry failed (https://host.test/a?token=error-secret): timeout"),
		"headers", map[string][]string{"Authorization": {"Bearer header-secret"}, "Referer": {"https://site.test/watch?session=ref-secret"}},
		"group", slog.GroupValue(slog.String("password", "group-secret")), "status", 503)
	for _, out := range []string{a.String(), b.String()} {
		for _, secret := range []string{"token-secret", "url-secret", "error-secret", "header-secret", "ref-secret", "group-secret", "user:pass", "#fragment"} {
			if strings.Contains(out, secret) {
				t.Errorf("leaked %q in %s", secret, out)
			}
		}
		if !strings.Contains(out, "timeout") || !strings.Contains(out, "host.test/video") || !strings.Contains(out, "503") {
			t.Fatalf("useful context lost: %s", out)
		}
	}
}

func TestCyclicLogValuesAreBounded(t *testing.T) {
	m := map[string]any{}
	m["self"] = m
	var buf bytes.Buffer
	slog.New(Redact(slog.NewJSONHandler(&buf, nil))).Info("cycle", "value", m)
	if !strings.Contains(buf.String(), "nested value omitted") {
		t.Fatal("cycle not bounded")
	}
}

func TestSensitiveGroupNamesRedactTheirValues(t *testing.T) {
	var buf bytes.Buffer
	slog.New(Redact(slog.NewJSONHandler(&buf, nil))).WithGroup("authorization").With("scheme", "secret-scheme").Info("request", "value", "secret-value")
	if strings.Contains(buf.String(), "secret-") {
		t.Fatal(buf.String())
	}
}

func TestURLValuesAreRedactedWithoutRelyingOnPointerStringer(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(Redact(slog.NewJSONHandler(&buf, nil)))
	parsed, _ := url.Parse("https://user:secret@host/video?token=secret#secret")
	logger.Info("URL", "value", *parsed, "pointer", parsed)
	if strings.Contains(buf.String(), "secret") {
		t.Fatal(buf.String())
	}
}

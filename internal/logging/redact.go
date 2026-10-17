// Package logging redacts credentials before logs reach stderr or telemetry.
package logging

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"reflect"
	"regexp"
	"strings"
)

var webURL = regexp.MustCompile(`(?i)https?://[^\s"'<>]+`)

// Text preserves error context and URL paths while removing credentials,
// signed query parameters and fragments, which frequently carry session tokens.
func Text(text string) string {
	return webURL.ReplaceAllStringFunc(text, func(raw string) string {
		suffix := ""
		for len(raw) > 0 && strings.ContainsRune(").,;]}", rune(raw[len(raw)-1])) {
			suffix = raw[len(raw)-1:] + suffix
			raw = raw[:len(raw)-1]
		}
		u, err := url.Parse(raw)
		if err != nil {
			return "[invalid URL redacted]" + suffix
		}
		u.User, u.RawQuery, u.Fragment, u.RawFragment, u.ForceQuery = nil, "", "", "", false
		return u.String() + suffix
	})
}

func sensitive(key string) bool {
	key = strings.NewReplacer("-", "", "_", "", ".", "").Replace(strings.ToLower(key))
	if key == "auth" {
		return true
	}
	for _, suffix := range []string{"authorization", "cookie", "password", "token", "secret", "apikey", "credentials", "credential", "privatekey", "accesskey"} {
		if strings.HasSuffix(key, suffix) {
			return true
		}
	}
	return false
}

func value(v any) any { return nestedValue(v, 0) }

func nestedValue(v any, depth int) any {
	if depth > 8 {
		return "[nested value omitted]"
	}
	switch x := v.(type) {
	case string:
		return Text(x)
	case url.URL:
		return Text(x.String())
	case *url.URL:
		if x == nil {
			return nil
		}
		return Text(x.String())
	case error:
		return Text(x.Error())
	case fmt.Stringer:
		return Text(x.String())
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Map:
		if rv.Type().Key().Kind() != reflect.String {
			return v
		}
		out := make(map[string]any, rv.Len())
		it := rv.MapRange()
		for it.Next() {
			key := it.Key().String()
			if sensitive(key) {
				out[key] = "[redacted]"
			} else {
				out[key] = nestedValue(it.Value().Interface(), depth+1)
			}
		}
		return out
	case reflect.Slice, reflect.Array:
		out := make([]any, rv.Len())
		for i := range out {
			out[i] = nestedValue(rv.Index(i).Interface(), depth+1)
		}
		return out
	}
	return v
}

func attr(a slog.Attr) slog.Attr {
	if sensitive(a.Key) {
		return slog.String(a.Key, "[redacted]")
	}
	a.Value = a.Value.Resolve()
	switch a.Value.Kind() {
	case slog.KindString:
		a.Value = slog.StringValue(Text(a.Value.String()))
	case slog.KindAny:
		a.Value = slog.AnyValue(value(a.Value.Any()))
	case slog.KindGroup:
		children := a.Value.Group()
		clean := make([]slog.Attr, len(children))
		for i, child := range children {
			clean[i] = attr(child)
		}
		a.Value = slog.GroupValue(clean...)
	}
	return a
}

type redacting struct {
	next        slog.Handler
	secretGroup bool
}

// Redact wraps every destination, including OTLP, before records leave process.
func Redact(next slog.Handler) slog.Handler { return redacting{next: next} }
func (h redacting) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}
func (h redacting) Handle(ctx context.Context, r slog.Record) error {
	clean := slog.NewRecord(r.Time, r.Level, Text(r.Message), r.PC)
	r.Attrs(func(a slog.Attr) bool { clean.AddAttrs(h.attribute(a)); return true })
	return h.next.Handle(ctx, clean)
}
func (h redacting) WithAttrs(attrs []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		clean[i] = h.attribute(a)
	}
	return redacting{next: h.next.WithAttrs(clean), secretGroup: h.secretGroup}
}
func (h redacting) WithGroup(name string) slog.Handler {
	return redacting{next: h.next.WithGroup(name), secretGroup: h.secretGroup || sensitive(name)}
}

func (h redacting) attribute(a slog.Attr) slog.Attr {
	if h.secretGroup {
		return slog.String(a.Key, "[redacted]")
	}
	return attr(a)
}

package mediaserver

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
)

type originTransport func(*http.Request) (*http.Response, error)

func (f originTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestBackendKeepsOriginCredentialsWithinEachCast(t *testing.T) {
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	http.DefaultTransport = originTransport(func(r *http.Request) (*http.Response, error) {
		h := make(http.Header)
		if r.URL.Path == "/login" {
			h.Set("Set-Cookie", "session=cast-A; Path=/")
		}
		return &http.Response{StatusCode: 200, Header: h, Body: io.NopCloser(strings.NewReader(r.Header.Get("Cookie"))), Request: r}, nil
	})
	cfg := defaults()
	create := cfg.backend()
	a := create(&mediav1.PlaybackSettings{}).(caster)
	b := create(&mediav1.PlaybackSettings{}).(caster)
	u, _ := url.Parse("https://origin.test/login")
	if _, _, _, err := a.engine.client.Fetch(t.Context(), u, nil); err != nil {
		t.Fatal(err)
	}
	u.Path = "/media"
	body, _, _, err := b.engine.client.Fetch(t.Context(), u, http.Header{"Cookie": {"session=cast-B"}})
	if err != nil || body != "session=cast-B" {
		t.Fatalf("cast B sent %q: %v", body, err)
	}
	body, _, _, err = a.engine.client.Fetch(t.Context(), u, nil)
	if err != nil || body != "session=cast-A" {
		t.Fatalf("cast A lost its own session: %q: %v", body, err)
	}
}

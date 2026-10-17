package transport

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestBearerDoesNotFollowRedirectsToAnotherServer(t *testing.T) {
	var leaked atomic.Bool
	server := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "other.example" {
			leaked.Store(r.Header.Get("Authorization") != "")
			return
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("configured server did not receive its bearer token")
		}
		http.Redirect(w, r, "http://other.example/", http.StatusTemporaryRedirect)
	}))
	// One in-memory HTTP server models two origins; the real HTTP client still
	// owns redirect handling and the production transport adds credentials.
	previous := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = previous })
	response, err := Bearer("secret").Get("http://first.example/")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if leaked.Load() {
		t.Fatal("redirect forwarded bearer credentials")
	}
	if response.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("redirect status = %d", response.StatusCode)
	}
}

package web

import (
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestChromeTransportReadsHTTPSOverBothProtocols(t *testing.T) {
	for name, h2 := range map[string]bool{"http/1.1": false, "h2": true} {
		t.Run(name, func(t *testing.T) {
			origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, r.Proto)
			}))
			origin.EnableHTTP2 = h2
			origin.StartTLS()
			defer origin.Close()

			// The origin's certificate is self-signed: trust it as a system root would be.
			pool := x509.NewCertPool()
			pool.AddCert(origin.Certificate())
			transport := newChromeTransport()
			transport.rootCAs = pool

			for range 2 { // The second read is the first's learned protocol, reused.
				resp, err := (&http.Client{Transport: transport}).Get(origin.URL)
				if err != nil {
					t.Fatal(err)
				}
				body, _ := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				want := "HTTP/1.1"
				if h2 {
					want = "HTTP/2.0"
				}
				if string(body) != want {
					t.Errorf("served over %q, want %q", body, want)
				}
			}
		})
	}
}

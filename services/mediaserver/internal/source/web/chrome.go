package web

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"
)

// errSpeaksH2 is what the HTTP/1 transport's dial says when the origin chose HTTP/2 for a connection.
var errSpeaksH2 = errors.New("origin negotiated h2")

// chromeTransport reads origins over TLS that opens as Chrome's does. Some origins answer 403 to any other
// ClientHello however the request is dressed, and the browser is what got the link in the first place.
type chromeTransport struct {
	h1 *http.Transport
	h2 *http2.Transport

	// rootCAs is who the origins' certificates are verified against; nil is the system's.
	rootCAs *x509.CertPool

	mu sync.Mutex
	// speaksH2 is the hosts whose origin picked HTTP/2, which net/http cannot speak over a connection it did not dial itself.
	speaksH2 map[string]bool
}

// newTransport is how castor reads origins: as Chrome does, unless the program replaced http.DefaultTransport,
// which is then its own choice to respect (instrumentation, or a test standing in for an origin).
func newTransport() http.RoundTripper {
	if _, stock := http.DefaultTransport.(*http.Transport); !stock {
		return http.DefaultTransport
	}
	return newChromeTransport()
}

func newChromeTransport() *chromeTransport {
	t := &chromeTransport{speaksH2: map[string]bool{}}
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.DialTLSContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := t.dial(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		if conn.ConnectionState().NegotiatedProtocol == "h2" {
			_ = conn.Close()
			t.mu.Lock()
			t.speaksH2[addr] = true
			t.mu.Unlock()
			return nil, errSpeaksH2
		}
		return conn, nil
	}
	base.ForceAttemptHTTP2 = false
	t.h1 = base
	t.h2 = &http2.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			return t.dial(ctx, network, addr)
		},
	}
	return t
}

// dial opens a TLS connection to addr that offers what Chrome's ClientHello offers.
func (t *chromeTransport) dial(ctx context.Context, network, addr string) (*utls.UConn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	raw, err := (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	conn := utls.UClient(raw, &utls.Config{ServerName: host, RootCAs: t.rootCAs}, utls.HelloChrome_Auto)
	if err := conn.HandshakeContext(ctx); err != nil {
		_ = raw.Close()
		return nil, err
	}
	return conn, nil
}

func (t *chromeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" {
		return t.h1.RoundTrip(req)
	}
	addr := req.URL.Host
	if req.URL.Port() == "" {
		addr = net.JoinHostPort(req.URL.Hostname(), "443")
	}
	t.mu.Lock()
	h2 := t.speaksH2[addr]
	t.mu.Unlock()
	if !h2 {
		resp, err := t.h1.RoundTrip(req)
		if !errors.Is(err, errSpeaksH2) {
			return resp, err
		}
	}
	return t.h2.RoundTrip(req)
}

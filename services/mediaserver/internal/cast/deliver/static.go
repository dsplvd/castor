package deliver

import (
	"context"
	"fmt"
	"github.com/stupside/castor/services/mediaserver/internal/cast/deliver/httpserve"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Static serves one small document, such as a caption file, from its own listener until the returned func closes it.
// Cast receivers fetch captions as a web page would, so every response allows any origin.
func Static(ctx context.Context, listeners Listeners, name, contentType, body string) (*url.URL, func() error, error) {
	ln, err := listeners.Listen(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("opening the %s listener: %w", name, err)
	}
	server := httpserve.Start(ctx, ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Access-Control-Allow-Origin", "*")
		http.ServeContent(w, r, name, time.Time{}, strings.NewReader(body))
	}))
	u := &url.URL{Scheme: "http", Host: ln.Addr().String(), Path: "/" + name}
	return u, func() error { return httpserve.Stop(server) }, nil
}

package progressive

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stupside/castor/services/mediaserver/internal/cast/output/storage"

	"golang.org/x/sync/semaphore"
)

func liveSpool(t *testing.T, window int64) (*storage.Spool, *semaphore.Weighted) {
	t.Helper()
	budget := semaphore.NewWeighted(window * 2)
	s, err := storage.NewLiveSpoolWithBudget(filepath.Join(t.TempDir(), "live.ts"), window, budget)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s, budget
}

func TestLiveHTTPRetainedRangesAndExpiredReconnect(t *testing.T) {
	s, _ := liveSpool(t, 8)
	if _, err := s.Write([]byte("0123456789abcdef")); err != nil {
		t.Fatal(err)
	}
	stream := &Stream{spool: s}
	for _, tt := range []struct {
		rangeHeader  string
		status       int
		body         string
		contentRange string
	}{
		{"", http.StatusGone, "", ""},
		{"bytes=0-", http.StatusGone, "", ""},
		{"bytes=9-12", http.StatusPartialContent, "9abc", "bytes 9-12/*"},
		{"bytes=13-", http.StatusPartialContent, "def", "bytes 13-15/*"},
		{"bytes=99-", http.StatusRequestedRangeNotSatisfiable, "", ""},
	} {
		req := httptest.NewRequest(http.MethodGet, "http://example/stream", nil)
		req.Header.Set("Range", tt.rangeHeader)
		recorder := httptest.NewRecorder()
		stream.handleStream(recorder, req.WithContext(t.Context()))
		if recorder.Code != tt.status {
			t.Fatalf("%s status=%d", tt.rangeHeader, recorder.Code)
		}
		if tt.status == http.StatusPartialContent && (recorder.Body.String() != tt.body || recorder.Header().Get("Content-Range") != tt.contentRange) {
			t.Fatalf("%s body=%q range=%s", tt.rangeHeader, recorder.Body.String(), recorder.Header().Get("Content-Range"))
		}
	}
}

type advancingResponse struct {
	*httptest.ResponseRecorder
	advance func()
}

func (w *advancingResponse) Write(p []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(p)
	if w.advance != nil {
		advance := w.advance
		w.advance = nil
		advance()
	}
	return n, err
}

func TestLiveHTTPAbortsWhenReaderFallsOutOfWindow(t *testing.T) {
	s, _ := liveSpool(t, 8)
	_, _ = s.Write([]byte("0123"))
	stream := &Stream{spool: s}
	recorder := &advancingResponse{ResponseRecorder: httptest.NewRecorder(), advance: func() { _, _ = s.Write([]byte("456789abcdef")) }}
	defer func() {
		if got := recover(); got != http.ErrAbortHandler {
			t.Fatalf("expired response did not abort: %v", got)
		}
		if recorder.Body.String() != "0123" {
			t.Fatalf("mixed expired bytes into response: %q", recorder.Body.String())
		}
	}()
	stream.handleStream(recorder, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://example/stream", nil))
}

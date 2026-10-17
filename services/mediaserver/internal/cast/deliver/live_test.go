package deliver

import (
	"bytes"
	"context"
	"errors"
	"golang.org/x/sync/semaphore"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
)

func liveSpool(t *testing.T, window int64) (*Spool, *semaphore.Weighted) {
	t.Helper()
	budget := semaphore.NewWeighted(window * 2)
	s, err := NewLiveSpoolWithBudget(filepath.Join(t.TempDir(), "live.ts"), window, budget)
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

func TestLiveRingWrapPreservesAbsoluteBytesAndProbePrefix(t *testing.T) {
	s, budget := liveSpool(t, 8)
	if s.Path() != "" {
		t.Fatal("ring advertised as ordinary file")
	}
	for _, part := range []string{"0123", "4567", "89ab", "cdef"} {
		if _, err := s.Write([]byte(part)); err != nil {
			t.Fatal(err)
		}
	}
	s.CloseWrite(nil)
	if first, end := s.Retained(); first != 8 || end != 16 {
		t.Fatalf("retained = %d,%d", first, end)
	}
	if _, err := s.TailAt(t.Context(), 0); !errors.Is(err, ErrSpoolExpired) {
		t.Fatalf("expired = %v", err)
	}
	tail, err := s.TailAt(t.Context(), 9)
	if err != nil {
		t.Fatal(err)
	}
	defer tail.Close()
	got, err := io.ReadAll(tail)
	if err != nil || string(got) != "9abcdef" {
		t.Fatalf("wrapped bytes = %q, %v", got, err)
	}
	prefix, err := os.ReadFile(s.ProbePath())
	if err != nil {
		t.Fatal(err)
	}
	if string(prefix) != "01234567" {
		t.Fatalf("probe prefix = %q", prefix)
	}
	assertDiskAvailable(t, budget, 0)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	assertDiskAvailable(t, budget, 16)
}

func TestLiveRingFastReaderSurvivesManyWrapsSlowReaderExpires(t *testing.T) {
	s, _ := liveSpool(t, 16)
	fast, err := s.TailAt(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer fast.Close()
	slow, err := s.TailAt(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer slow.Close()
	for i := range 10000 {
		want := bytes.Repeat([]byte{byte(i)}, 7)
		if _, err := s.Write(want); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, len(want))
		if _, err := io.ReadFull(fast, got); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("corrupted data at wrap%d", i)
		}
	}
	if _, err := slow.Read(make([]byte, 1)); !errors.Is(err, ErrSpoolExpired) {
		t.Fatalf("slow reader = %v", err)
	}
	info, err := os.Stat(s.ProbePath() + ".ring")
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 16 {
		t.Fatalf("ring grew to %d", info.Size())
	}
}

func TestLiveRingParkedReaderCancels(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, _ := liveSpool(t, 8)
		ctx, cancel := context.WithCancel(t.Context())
		tail, err := s.TailAt(ctx, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer tail.Close()
		result := make(chan error, 1)
		go func() { _, err := tail.Read(make([]byte, 1)); result <- err }()
		synctest.Wait()
		cancel()
		if err := <-result; !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel = %v", err)
		}
	})
}

func TestLiveHTTPRetainedRangesAndExpiredReconnect(t *testing.T) {
	s, _ := liveSpool(t, 8)
	if _, err := s.Write([]byte("0123456789abcdef")); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	stream := &Stream{spool: s, done: done}
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
		stream.handleStream(t.Context(), recorder, req)
		if recorder.Code != tt.status {
			t.Fatalf("%s status=%d", tt.rangeHeader, recorder.Code)
		}
		if tt.status == http.StatusPartialContent && (recorder.Body.String() != tt.body || recorder.Header().Get("Content-Range") != tt.contentRange) {
			t.Fatalf("%s body=%q range=%s", tt.rangeHeader, recorder.Body.String(), recorder.Header().Get("Content-Range"))
		}
	}
}

func TestLiveRingReservesCapacityBeforeCreatingFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "live")
	budget := semaphore.NewWeighted(15)
	if _, err := NewLiveSpoolWithBudget(path, 8, budget); !errors.Is(err, ErrSpoolLimit) {
		t.Fatalf("admission = %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("refused spool exists: %v", err)
	}
	assertDiskAvailable(t, budget, 15)
}

func TestLiveReaderAdmissionIsBoundedAndReleased(t *testing.T) {
	s, _ := liveSpool(t, 8)
	var readers []io.ReadCloser
	for range MaxSpoolReaders {
		r, err := s.TailAt(t.Context(), 0)
		if err != nil {
			t.Fatal(err)
		}
		readers = append(readers, r)
	}
	if _, err := s.TailAt(t.Context(), 0); !errors.Is(err, ErrSpoolReaders) {
		t.Fatalf("reader admission = %v", err)
	}
	for _, r := range readers {
		if err := r.Close(); err != nil {
			t.Fatal(err)
		}
	}
	r, err := s.TailAt(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
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
	stream := &Stream{spool: s, done: make(chan struct{})}
	recorder := &advancingResponse{ResponseRecorder: httptest.NewRecorder(), advance: func() { _, _ = s.Write([]byte("456789abcdef")) }}
	defer func() {
		if got := recover(); got != http.ErrAbortHandler {
			t.Fatalf("expired response did not abort: %v", got)
		}
		if recorder.Body.String() != "0123" {
			t.Fatalf("mixed expired bytes into response: %q", recorder.Body.String())
		}
	}()
	stream.handleStream(t.Context(), recorder, httptest.NewRequest(http.MethodGet, "http://example/stream", nil))
}

package whisper

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestConcurrentDownloadsPublishOneCompleteModel(t *testing.T) {
	const readers = 8
	var ready sync.WaitGroup
	ready.Add(readers)
	payload := bytes.Repeat([]byte("model data"), 64<<10)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ready.Done()
		ready.Wait()
		_, _ = w.Write(payload)
	}))
	t.Cleanup(origin.Close)
	dest := filepath.Join(t.TempDir(), "model.bin")
	results := make(chan error, readers)
	for range readers {
		go func() { results <- downloadFile(t.Context(), origin.URL, dest, digest(payload)) }()
	}
	for range readers {
		if err := <-results; err != nil {
			t.Errorf("download = %v", err)
		}
	}
	got, err := os.ReadFile(dest)
	if err != nil || !bytes.Equal(got, payload) {
		t.Errorf("published model has %d bytes (%v), want one complete download", len(got), err)
	}
}

func digest(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }

func TestDownloadRefusesWhatDoesNotMatchItsChecksum(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("tampered")) }))
	t.Cleanup(origin.Close)
	dest := filepath.Join(t.TempDir(), "model.bin")
	if err := downloadFile(t.Context(), origin.URL, dest, digest([]byte("genuine"))); err == nil {
		t.Error("download of the wrong bytes succeeded")
	}
	if _, err := os.Stat(dest); err == nil {
		t.Error("a model that failed its checksum was published")
	}
}

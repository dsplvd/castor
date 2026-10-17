package deliver

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/container"
	"github.com/stupside/castor/services/mediaserver/internal/media"
)

// Handed is the most one client took: a HEAD takes nothing, and a later partial client does not add to it.
func TestHandedIsTheMostAnyOneClientTook(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const size = 8 << 20
		srv := spooled(t, Opening{}, payload(size))

		head(t, srv)
		if handed, last := srv.Handed(); handed != 0 || !last.IsZero() {
			t.Fatalf("a HEAD probe was credited with %d bytes at %v", handed, last)
		}

		// Read to EOF: the in-memory pipe has no socket buffer to absorb the chunked trailer.
		if whole, err := io.ReadAll(get(t, srv, "").Body); err != nil || len(whole) != size {
			t.Fatalf("a whole client took %d bytes (%v), want %d", len(whole), err, size)
		}
		partial := get(t, srv, "")
		read(t, partial, 1)
		_ = partial.Body.Close()
		settled(t, srv)
		if handed, last := srv.Handed(); handed != size || last.IsZero() {
			t.Errorf("handed = %d at %v, want the %d bytes one client took", handed, last, size)
		}
	})
}

// A resumed client is served its range and credited with the prefix it already had.
func TestARangeResumesFromTheByteAsked(t *testing.T) {
	want := payload(1 << 20)
	total := int64(len(want))
	from := total / 2
	for _, tt := range []struct {
		name     string
		asked    string
		to       int64
		finishes bool
	}{
		{"open-ended", fmt.Sprintf("bytes=%d-", from), total - 1, true},
		{"bounded to the last byte", fmt.Sprintf("bytes=%d-%d", from, total-1), total - 1, true},
		{"bounded short of the end", fmt.Sprintf("bytes=%d-%d", from, total-1024), total - 1024, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				srv := spooled(t, Opening{}, want)
				resp := get(t, srv, tt.asked)
				if resp.StatusCode != http.StatusPartialContent {
					t.Fatalf("status = %d, want 206", resp.StatusCode)
				}
				if got, stated := resp.Header.Get("Content-Range"), fmt.Sprintf("bytes %d-%d/%d", from, tt.to, total); got != stated {
					t.Errorf("Content-Range = %q, want %q", got, stated)
				}
				got, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, want[from:tt.to+1]) {
					t.Errorf("handed %d bytes, want exactly bytes %d-%d", len(got), from, tt.to)
				}
				if !tt.finishes {
					return
				}
				settled(t, srv)
				if handed, _ := srv.Handed(); handed != total {
					t.Errorf("handed = %d, want the whole %d the resumed client holds", handed, total)
				}
			})
		})
	}
}

func TestAResumePastTheEndIs416(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		want := payload(4096)
		resp := get(t, spooled(t, Opening{}, want), fmt.Sprintf("bytes=%d-", len(want)))
		if resp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
			t.Fatalf("status = %d, want 416", resp.StatusCode)
		}
		if got, stated := resp.Header.Get("Content-Range"), fmt.Sprintf("bytes */%d", len(want)); got != stated {
			t.Errorf("Content-Range = %q, want %q", got, stated)
		}
	})
}

// A range nobody can honour is replayed from byte 0 rather than answered with an invented length.
func TestARefusedRangeReplaysFromByteZero(t *testing.T) {
	body := payload(64 << 10)
	for _, tt := range []struct {
		name string
		srv  func(t *testing.T) *Stream
	}{
		{"a delivery declaring Accept-Ranges: none", func(t *testing.T) *Stream {
			return spooled(t, Opening{Headers: map[string]string{"Accept-Ranges": "none"}}, body)
		}},
		{"a producer still running", func(t *testing.T) *Stream { return producing(t, body) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				resp := get(t, tt.srv(t), fmt.Sprintf("bytes=%d-", len(body)/2))
				if resp.StatusCode != http.StatusOK {
					t.Fatalf("status = %d, want 200", resp.StatusCode)
				}
				if got := read(t, resp, len(body)); !bytes.Equal(got, body) {
					t.Error("the refused client was not served from byte 0")
				}
			})
		})
	}
}

// A client that stops reading is cut by the write deadline, and Wait ends after the idle grace.
func TestAHungClientIsFreedAndTheCastEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := spooled(t, Opening{WriteDeadline: 300 * time.Millisecond, IdleGrace: 50 * time.Millisecond}, payload(8<<20))
		read(t, get(t, srv, ""), 1)
		settled(t, srv)
		srv.audience.mu.Lock()
		defer srv.audience.mu.Unlock()
		if srv.audience.completed {
			t.Error("a client read to EOF, so the grace this test exists to reach was never consulted")
		}
	})
}

// A spool another writes is replayed from byte 0, resumed once its writer is done, and let go of without waiting for that writer.
func TestASpoolAnotherWritesIsServedWithoutBeingOwned(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		body := payload(1 << 20)
		sp, err := NewSpool(filepath.Join(t.TempDir(), "spool.ts"))
		if err != nil {
			t.Fatal(err)
		}
		half := len(body) / 2
		if _, err := sp.Write(body[:half]); err != nil {
			t.Fatal(err)
		}
		drained := make(chan struct{})
		devices := newLAN(t)
		srv, err := OpenSpooledStream(t.Context(), Opening{
			Format:        container.Format{ContentType: media.MPEGTS, Extension: ".ts"},
			Listeners:     devices,
			WriteDeadline: time.Minute,
		}, sp, drained)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = srv.Close() })

		whole := get(t, srv, "")
		if got := read(t, whole, half); !bytes.Equal(got, body[:half]) {
			t.Error("a client of a spool still being written was not served it from byte 0")
		}
		if resp := get(t, srv, fmt.Sprintf("bytes=%d-", half)); resp.StatusCode != http.StatusOK {
			t.Errorf("a range asked while the writer runs answered %d, want the 200 replay", resp.StatusCode)
		}

		if _, err := sp.Write(body[half:]); err != nil {
			t.Fatal(err)
		}
		sp.CloseWrite(nil)
		close(drained)
		if got, err := io.ReadAll(whole.Body); err != nil || !bytes.Equal(got, body[half:]) {
			t.Errorf("the replaying client got %d more bytes (%v), want the rest the writer added", len(got), err)
		}
		resumed := get(t, srv, fmt.Sprintf("bytes=%d-", half))
		if resumed.StatusCode != http.StatusPartialContent {
			t.Fatalf("a range asked once the writer is done answered %d, want 206", resumed.StatusCode)
		}
		if got, _ := io.ReadAll(resumed.Body); !bytes.Equal(got, body[half:]) {
			t.Errorf("the resumed client got %d bytes, want the %d from the byte it asked", len(got), len(body)-half)
		}

		running, err := NewSpool(filepath.Join(t.TempDir(), "running.ts"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { running.CloseWrite(nil) })
		unowned, err := OpenSpooledStream(t.Context(), Opening{Listeners: devices}, running, make(chan struct{}))
		if err != nil {
			t.Fatal(err)
		}
		closed := make(chan error, 1)
		go func() { closed <- unowned.Close() }()
		synctest.Wait()
		select {
		case <-closed:
		default:
			t.Fatal("Close waited for a writer the server does not own")
		}
	})
}

func payload(size int) []byte {
	out := make([]byte, size)
	for i := range out {
		out[i] = byte(i % 251)
	}
	return out
}

// spooled starts a server and returns once the whole body is in the spool.
func spooled(t *testing.T, o Opening, body []byte) *Stream {
	t.Helper()
	o.Listeners = newLAN(t)
	o.Format = container.Format{ContentType: "video/mp4", Extension: ".mp4"}
	o.WriteDeadline = cmp.Or(o.WriteDeadline, time.Minute)
	o.IdleGrace = cmp.Or(o.IdleGrace, 30*time.Second)
	srv, err := OpenStream(t.Context(), o, t.TempDir(), bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	<-srv.Drained()
	return srv
}

// producing serves a head the producer has written while it is still running.
func producing(t *testing.T, head []byte) *Stream {
	t.Helper()
	pr, pw := io.Pipe()
	srv, err := OpenStream(t.Context(), Opening{
		Format:        container.Format{ContentType: "video/mp4", Extension: ".mp4"},
		Listeners:     newLAN(t),
		WriteDeadline: time.Minute,
	}, t.TempDir(), pr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = pw.Close()
		_ = srv.Close()
	})
	if _, err := pw.Write(head); err != nil {
		t.Fatal(err)
	}
	synctest.Wait()
	if landed, _ := srv.spooled(); landed < int64(len(head)) {
		t.Fatal("the head never reached the spool")
	}
	return srv
}

// settled waits for every client to be accounted for.
func settled(t *testing.T, srv *Stream) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := srv.Wait(ctx); err != nil {
		t.Fatalf("Wait = %v, want nil once every client is gone", err)
	}
}

func get(t *testing.T, srv *Stream, byteRange string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if byteRange != "" {
		req.Header.Set("Range", byteRange)
	}
	resp, err := device(srv.o).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func read(t *testing.T, resp *http.Response, n int) []byte {
	t.Helper()
	got := make([]byte, n)
	if _, err := io.ReadFull(resp.Body, got); err != nil {
		t.Fatalf("reading the served stream: %v", err)
	}
	return got
}

func head(t *testing.T, srv *Stream) int {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodHead, srv.URL().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := device(srv.o).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// A writer that cannot take a deadline is warned about once, not on every chunk.
func TestAMissingWriteDeadlineIsWarnedOnce(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	sp := newTestSpool(t)
	if _, err := sp.Write(payload(3 * sendChunkSize)); err != nil {
		t.Fatal(err)
	}
	sp.CloseWrite(nil)
	done := make(chan struct{})
	close(done)
	s := &Stream{spool: sp, done: done, o: Opening{WriteDeadline: time.Second}}
	tail, err := sp.TailAt(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer tail.Close()

	s.send(t.Context(), httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil), served{code: http.StatusOK, end: -1, total: -1}, tail, nil)
	if got := strings.Count(logs.String(), "write deadline unavailable"); got != 1 {
		t.Errorf("warned %d times over 3 chunks, want once", got)
	}
}

func TestStreamCloseInterruptsBlockedProducer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		reader, writer := io.Pipe()
		defer writer.Close()
		srv, err := OpenStream(t.Context(), Opening{Listeners: newLAN(t)}, t.TempDir(), reader)
		if err != nil {
			t.Fatal(err)
		}
		if err := srv.Close(); err != nil {
			t.Fatal(err)
		}
		select {
		case <-srv.Drained():
		default:
			t.Fatal("producer remains blocked after close")
		}
	})
}

type cleanupReader struct {
	io.Reader
	close func() error
}

func (r cleanupReader) Close() error { return r.close() }

func TestStreamCloseJoinsProducerCleanup(t *testing.T) {
	closing, release := make(chan struct{}), make(chan struct{})
	producer := cleanupReader{Reader: strings.NewReader(""), close: func() error {
		close(closing)
		<-release
		return nil
	}}
	srv, err := OpenStream(t.Context(), Opening{Listeners: newLAN(t)}, t.TempDir(), producer)
	if err != nil {
		t.Fatal(err)
	}
	<-closing
	done := make(chan error, 1)
	go func() { done <- srv.Close() }()
	select {
	case <-done:
		t.Fatal("Close returned before producer cleanup")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestStreamCancellationPreservesUnrelatedProducerFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		reader, writer := io.Pipe()
		// Expose only Read: cancel cannot manufacture the error being asserted.
		srv, err := OpenStream(ctx, Opening{Listeners: newLAN(t)}, t.TempDir(), struct{ io.Reader }{reader})
		if err != nil {
			t.Fatal(err)
		}
		want := errors.New("producer data corrupted")
		cancel()
		writer.CloseWithError(want)
		if err := srv.Close(); !errors.Is(err, want) {
			t.Fatalf("Close() = %v, want producer failure", err)
		}
	})
}

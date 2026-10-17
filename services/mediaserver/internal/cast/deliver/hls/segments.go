package hls

import (
	"context"
	"errors"
	"fmt"
	"github.com/stupside/castor/services/mediaserver/internal/cast/deliver"
	"github.com/stupside/castor/services/mediaserver/internal/cast/deliver/httpserve"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/subtitle"
)

// Segments serves dir over HTTP, tracks liveness for Wait, deletes behind live edge.
type Segments struct {
	o        deliver.Opening
	listener net.Listener
	server   *http.Server
	root     *os.Root
	playlist string
	captions *captionDelivery

	done         chan struct{}
	finish       func()
	guardStop    context.CancelFunc
	producerStop context.CancelFunc
	guard        sync.WaitGroup
	storageErr   error
	served       atomic.Int64 // Artifacts handed over (measure of device fetch, not bytes).
	reader       sync.WaitGroup
	requests     sync.RWMutex
	lifetime     context.Context

	mu          sync.Mutex
	lastRequest time.Time // When device last requested anything (seeded at New).
}

// Open serves the live HLS directory dir producer writes; no byte pacing, the client self-paces.
func Open(ctx context.Context, o deliver.Opening, dir string, producer io.Reader) (*Segments, error) {
	return openSegments(ctx, o, dir, nil, func(ctx context.Context, _ *os.Root) error {
		if closer, ok := producer.(io.Closer); ok {
			closeProducer := sync.OnceFunc(func() { _ = closer.Close() })
			stop := context.AfterFunc(ctx, closeProducer)
			defer stop()
			defer closeProducer()
		}
		_, err := io.Copy(io.Discard, producer)
		return err
	})
}

// OpenCaptions publishes a bounded HLS rendition from fragmented MP4.
// It owns the producer reader and waits for finalized captions before publishing
// each segment. videoOffset is the first video's position on the transcript clock.
func OpenCaptions(ctx context.Context, o deliver.Opening, dir string, producer io.ReadCloser, cues *subtitle.Cues, videoOffset time.Duration) (*Segments, error) {
	if cues == nil || producer == nil {
		return nil, fmt.Errorf("caption HLS requires a producer and transcript")
	}
	o.Format = format()
	captions := &captionDelivery{cues: cues, videoOffset: videoOffset, cache: make(map[string]captionSegment)}
	return openSegments(ctx, o, dir, captions, func(ctx context.Context, root *os.Root) error {
		return publishCaptionHLS(ctx, root, producer, cues, videoOffset)
	})
}

func openSegments(ctx context.Context, o deliver.Opening, dir string, captions *captionDelivery, produce func(context.Context, *os.Root) error) (*Segments, error) {
	ln, err := o.Listeners.Listen(ctx)
	if err != nil {
		return nil, fmt.Errorf("starting HLS server: %w", err)
	}
	// A root, so a symlink in the work dir never serves a file outside it.
	root, err := os.OpenRoot(dir)
	if err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("opening the HLS directory: %w", err)
	}

	s := &Segments{
		o:           o,
		listener:    ln,
		root:        root,
		playlist:    filepath.Join(dir, o.Format.Tuning.Output),
		done:        make(chan struct{}),
		lastRequest: time.Now(),
	}
	if captions != nil {
		captions.root, captions.playlist = root, o.Format.Tuning.Output
		s.captions = captions
	}
	guardCtx, guardStop := context.WithCancel(context.WithoutCancel(ctx))
	s.guardStop = guardStop
	producerCtx, producerStop := context.WithCancel(ctx)
	s.producerStop = producerStop
	s.lifetime = producerCtx
	s.finish = sync.OnceFunc(func() { close(s.done) })
	s.guard.Go(func() { s.watchDirectory(guardCtx, dir) })
	s.reader.Go(func() {
		// Drain to EOF (unread pipe stops ffmpeg once kernel buffer fills).
		defer s.finish()
		err := produce(producerCtx, root)
		if producerCtx.Err() != nil && (errors.Is(err, producerCtx.Err()) || errors.Is(err, io.ErrClosedPipe) || errors.Is(err, os.ErrClosed)) {
			return
		}
		if err != nil {
			slog.ErrorContext(producerCtx, "publishing HLS failed", "error", err)
			s.mu.Lock()
			s.storageErr = errors.Join(s.storageErr, err)
			s.mu.Unlock()
			if s.o.Abort != nil {
				s.o.Abort()
			}
		}
	})

	files := http.FileServerFS(root.FS())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		s.requests.RLock()
		defer s.requests.RUnlock()
		if s.lifetime.Err() != nil {
			http.Error(w, "delivery closed", http.StatusServiceUnavailable)
			return
		}
		s.mu.Lock()
		s.lastRequest = time.Now()
		s.mu.Unlock()
		if s.o.WriteDeadline > 0 {
			control := http.NewResponseController(w)
			if err := control.SetWriteDeadline(time.Now().Add(s.o.WriteDeadline)); err != nil {
				slog.WarnContext(r.Context(), "HLS write deadline unavailable", "error", err)
			}
			defer control.SetWriteDeadline(time.Time{})
		}
		// Set device headers first, then artifact's own type.
		for k, v := range s.o.Headers {
			w.Header().Set(k, v)
		}
		if s.captions != nil && s.captions.serve(w, r) {
			return
		}
		// Go doesn't register .m3u8/.m4s; set type before ServeContent sniffs.
		if ct, ok := artifactContentType(r.URL.Path); ok {
			w.Header().Set("Content-Type", ct)
		}
		// Count from answer, not request (404 on window-rolled segments shouldn't count).
		answer := &answered{ResponseWriter: w}
		files.ServeHTTP(answer, r)
		if r.Method == http.MethodGet && answer.carriedBytes() && isMediaArtifact(strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")) {
			s.served.Add(1)
		}
	})
	s.server = httpserve.Start(ctx, ln, mux)
	return s, nil
}

// URL is the media-playlist address the device should play.
func (s *Segments) URL() *url.URL {
	name := s.o.Format.Tuning.Output
	if s.captions != nil {
		name = "master.m3u8"
	}
	return &url.URL{Scheme: "http", Host: s.listener.Addr().String(), Path: "/" + name}
}

// Served returns artifacts handed over (zero = URL accepted but no bytes fetched).
func (s *Segments) Served() int { return int(s.served.Load()) }

// Drained closes when publication ends or delivery fails.
func (s *Segments) Drained() <-chan struct{} { return s.done }

func (s *Segments) Artifact() deliver.Artifact {
	// No patience (zero-byte m3u8 is unparseable).
	landed := func() int64 {
		info, err := os.Stat(s.playlist)
		if err != nil {
			return 0
		}
		return info.Size()
	}
	if s.captions != nil {
		landed = func() int64 {
			s.captions.mu.Lock()
			defer s.captions.mu.Unlock()
			video, _, err := s.captions.playlists()
			if err != nil {
				if errors.Is(err, errCaptionWindow) && s.o.Abort != nil {
					slog.Error("HLS caption delivery cannot recover its timeline", "error", err)
					s.o.Abort()
				}
				return 0
			}
			return int64(len(video))
		}
	}
	return deliver.Artifact{Path: s.playlist, Subject: "the HLS playlist", Landed: landed}
}

// answered tracks response status for Served count (ResponseWriter doesn't report it).
type answered struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (a *answered) WriteHeader(code int) {
	if a.status == 0 {
		// First status is the one sent; second WriteHeader never reaches wire.
		a.status = code
	}
	a.ResponseWriter.WriteHeader(code)
}

func (a *answered) Write(b []byte) (int, error) {
	if a.status == 0 {
		// net/http's implicit 200
		a.status = http.StatusOK
	}
	n, err := a.ResponseWriter.Write(b)
	a.bytes += int64(n)
	return n, err
}

// ReadFrom keeps file server's ReaderFrom (sendfile, not userspace copy).
func (a *answered) ReadFrom(r io.Reader) (int64, error) {
	if a.status == 0 {
		a.status = http.StatusOK
	}
	n, err := io.Copy(a.ResponseWriter, r)
	a.bytes += n
	return n, err
}

// Unwrap lets http.ResponseController reach the connection's own flush and deadlines.
func (a *answered) Unwrap() http.ResponseWriter { return a.ResponseWriter }

// carriedBytes reports successful response (2xx); 404 and 416 hand over nothing.
func (a *answered) carriedBytes() bool { return a.status >= 200 && a.status < 300 && a.bytes > 0 }

// Wait blocks until the encoder's output ended and the device stopped asking, or ctx ends.
func (s *Segments) Wait(ctx context.Context) error {
	err := httpserve.Settle(ctx, s.done, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.storageErr != nil || time.Since(s.lastRequest) > s.o.IdleGrace
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	return errors.Join(err, s.storageErr)
}

// Close stops HTTP server and joins encoder-output-draining goroutine.
func (s *Segments) Close() error {
	// Cancel publishing and reject new requests before joining users of root.
	s.producerStop()
	err := httpserve.Stop(s.server)
	s.requests.Lock()
	defer s.requests.Unlock()
	s.reader.Wait()
	s.guardStop()
	s.guard.Wait()
	s.mu.Lock()
	storageErr := s.storageErr
	s.mu.Unlock()
	return errors.Join(err, storageErr, s.root.Close())
}

func (s *Segments) watchDirectory(ctx context.Context, dir string) {
	ticker := time.NewTicker(directoryCheckInterval)
	defer ticker.Stop()
	for {
		if err := checkDirectory(dir, SegmentDirectoryLimit, segmentFileLimit); err != nil {
			s.mu.Lock()
			s.storageErr = errors.Join(s.storageErr, err)
			s.mu.Unlock()
			s.finish()
			if s.o.Abort != nil {
				s.o.Abort()
			}
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

package deliver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/cast/deliver/httpserve"
)

const (
	// sendChunkSize is the per-connection read/write granularity.
	sendChunkSize = 32 * 1024

	// Startup timeout; generous because it catches early container failures.
	firstBytesTimeout = 10 * time.Second
)

// Stream spools producer output and replays from byte 0; URL after first byte/timeout.
type Stream struct {
	o Opening

	listener net.Listener
	server   *http.Server
	cancel   context.CancelFunc
	spool    *Spool

	done <-chan struct{} // producer fully spooled
	// release joins and closes an owned producer; nil for a borrowed spool.
	release func() error

	audience streamAudience
}

// OpenStream spools producer into dir in the background and serves that spool over HTTP, replaying from byte 0.
func OpenStream(ctx context.Context, o Opening, dir string, producer io.Reader) (*Stream, error) {
	openSpool := NewSpool
	if o.Live {
		openSpool = NewLiveSpool
	}
	sp, err := openSpool(filepath.Join(dir, "out"+o.Format.Extension))
	if err != nil {
		return nil, fmt.Errorf("starting stream server: %w", err)
	}
	done := make(chan struct{})
	producerCtx, producerCancel := context.WithCancel(ctx)
	release := func() error {
		producerCancel()
		<-done
		return errors.Join(sp.Err(), sp.Close())
	}
	s, err := listenStream(ctx, o, sp, done, release)
	if err != nil {
		producerCancel()
		_ = sp.Close()
		return nil, fmt.Errorf("starting stream server: %w", err)
	}

	go func() {
		defer close(done)
		defer producerCancel()
		if closer, ok := producer.(io.Closer); ok {
			closeProducer := sync.OnceFunc(func() { _ = closer.Close() })
			stop := context.AfterFunc(producerCtx, closeProducer)
			defer stop()
			defer closeProducer()
		}
		_, copyErr := io.Copy(sp, producer)
		if producerCtx.Err() != nil && (errors.Is(copyErr, producerCtx.Err()) || errors.Is(copyErr, io.ErrClosedPipe) || errors.Is(copyErr, os.ErrClosed)) {
			copyErr = nil // Closing the reader interrupted Read; unrelated failures remain terminal.
		}
		sp.CloseWrite(copyErr)
		slog.DebugContext(ctx, "stream fully spooled", "bytes", sp.Size(), "error", copyErr)
	}()
	return s, nil
}

// OpenSpooledStream serves a spool another writes, whole once drained closes; Close leaves that writer running.
func OpenSpooledStream(ctx context.Context, o Opening, sp *Spool, drained <-chan struct{}) (*Stream, error) {
	srv, err := listenStream(ctx, o, sp, drained, nil)
	if err != nil {
		return nil, fmt.Errorf("starting stream server: %w", err)
	}
	return srv, nil
}

// listenStream binds where o's listeners say and serves sp, final once done closes.
func listenStream(parent context.Context, o Opening, sp *Spool, done <-chan struct{}, release func() error) (*Stream, error) {
	ln, err := o.Listeners.Listen(parent)
	if err != nil {
		return nil, err
	}

	// Close alone ends the stream; the cast lends only its values, so what it logs keeps the cast_id.
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	s := &Stream{
		o:        o,
		listener: ln,
		cancel:   cancel,
		spool:    sp,
		done:     done,
		release:  release,
		audience: streamAudience{lastDisconnect: time.Now()},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /stream"+o.Format.Extension, func(w http.ResponseWriter, r *http.Request) {
		s.handleStream(ctx, w, r)
	})
	s.server = httpserve.Start(ctx, ln, mux)
	return s, nil
}

func (s *Stream) URL() *url.URL {
	return &url.URL{Scheme: "http", Host: s.listener.Addr().String(), Path: "/stream" + s.o.Format.Extension}
}

// Drained is closed when producer ended and spool has all output.
func (s *Stream) Drained() <-chan struct{} { return s.done }

// Handed returns most bytes to any one client and when byte last moved; safe over-count.
func (s *Stream) Handed() (int64, time.Time) {
	return s.audience.handed()
}

func (s *Stream) Artifact() Artifact {
	return Artifact{
		Path:    s.spool.ProbePath(),
		Subject: "the stream output",
		Landed:  func() int64 { n, _ := s.spooled(); return n },
		Grace:   firstBytesTimeout,
	}
}

func (s *Stream) Close() error {
	s.cancel()
	err := httpserve.Stop(s.server)
	if s.release != nil {
		err = errors.Join(err, s.release())
	}
	return err
}

func (s *Stream) Wait(ctx context.Context) error {
	err := httpserve.Settle(ctx, s.done, func() bool {
		return s.audience.settled(s.o.IdleGrace)
	})
	return errors.Join(err, s.spool.Err())
}

// served is the stretch of the stream one response carries, and the status that states it.
type served struct {
	// code is 200 (the whole stream from byte 0), 206 or 416 (a start past a known end).
	code int
	// start is the first byte this response carries, end the last one inclusive.
	start int64
	end   int64
	total int64
}

func (s *Stream) resume(rangeHeader string) (served, string) {
	if s.spool.Live() {
		return s.resumeLive(rangeHeader), ""
	}
	whole := served{code: http.StatusOK, start: 0, end: -1, total: -1}
	if rangeHeader == "" {
		return whole, ""
	}
	start, end, ok := byteRange(rangeHeader)
	if !ok {
		return whole, "the Range header is not a single bytes=first-last range"
	}
	if start == 0 && end < 0 {
		// The ordinary replay: the client is asking for exactly the bytes it is about to be given.
		return whole, ""
	}
	refuse := func(why string) (served, string) {
		if start == 0 {
			return whole, ""
		}
		return whole, why
	}
	if s.rangesDeclined() {
		return refuse("this delivery's own response headers declare Accept-Ranges: none")
	}
	total, final := s.spooled()
	switch {
	case !final:
		return refuse("the producer is still running, so no response can state where this stream ends")
	case start >= total:
		return served{code: http.StatusRequestedRangeNotSatisfiable, total: total}, ""
	case end >= 0:
		return served{code: http.StatusPartialContent, start: start, end: min(end, total-1), total: total}, ""
	default:
		return served{code: http.StatusPartialContent, start: start, end: total - 1, total: total}, ""
	}
}

// byteRange parses the one form a resume takes: a single "bytes=first-last", with the last byte optional.
func byteRange(header string) (start, end int64, ok bool) {
	spec, found := strings.CutPrefix(strings.TrimSpace(header), "bytes=")
	if !found || strings.Contains(spec, ",") {
		return 0, 0, false
	}
	first, last, found := strings.Cut(spec, "-")
	if !found {
		return 0, 0, false
	}
	start, err := strconv.ParseInt(strings.TrimSpace(first), 10, 64)
	if err != nil || start < 0 {
		return 0, 0, false
	}
	if strings.TrimSpace(last) == "" {
		return start, -1, true
	}
	end, err = strconv.ParseInt(strings.TrimSpace(last), 10, 64)
	if err != nil || end < start {
		return 0, 0, false
	}
	return start, end, true
}

func (s *Stream) rangesDeclined() bool {
	for k, v := range s.o.Headers {
		if http.CanonicalHeaderKey(k) == "Accept-Ranges" {
			return strings.EqualFold(strings.TrimSpace(v), "none")
		}
	}
	return false
}

// spooled is how many bytes the producer has written and whether that figure is final.
func (s *Stream) spooled() (int64, bool) {
	select {
	case <-s.done:
		return s.spool.Size(), true
	default:
		return s.spool.Size(), false
	}
}

// severed says why one client stopped being written to.
func (s *Stream) severed(ctx context.Context, r *http.Request, err error, held int64, stalled time.Duration) {
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		slog.InfoContext(ctx, "stream client disconnected", "from", r.RemoteAddr, "bytes_sent", held, "error", err)
		return
	}
	_, final := s.spooled()
	slog.WarnContext(ctx, "severed a client that stopped draining the stream; unless it reconnects with a Range it restarts the program from the beginning",
		"from", r.RemoteAddr,
		"user_agent", r.UserAgent(),
		"stalled_for", stalled.Round(time.Millisecond),
		"write_deadline", s.o.WriteDeadline,
		"bytes_sent", held,
		"resumable", final && !s.rangesDeclined(),
	)
}

func (s *Stream) handleStream(srvCtx context.Context, w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	unhook := context.AfterFunc(srvCtx, cancel)
	defer unhook()

	s.writeHeaders(w)
	if r.Method == http.MethodHead {
		slog.InfoContext(ctx, "stream HEAD", "from", r.RemoteAddr, "user_agent", r.UserAgent())
		w.WriteHeader(http.StatusOK)
		return
	}

	sv, ok := s.admit(ctx, w, r)
	if !ok {
		return
	}

	tail, err := s.spool.TailAt(ctx, sv.start)
	if err != nil {
		if errors.Is(err, ErrSpoolExpired) {
			http.Error(w, err.Error(), http.StatusGone)
		} else {
			http.Error(w, "stream unavailable", http.StatusServiceUnavailable)
		}
		return
	}
	defer tail.Close()

	reachedEOF := false
	left := s.audience.joined()
	defer func() { left(reachedEOF) }()

	flusher := s.respond(w, sv)
	reachedEOF = s.send(ctx, w, r, sv, tail, flusher)
}

func (s *Stream) writeHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", s.o.Format.ContentType)
	for k, v := range s.o.Headers {
		w.Header().Set(k, v)
	}
}

func (s *Stream) admit(ctx context.Context, w http.ResponseWriter, r *http.Request) (served, bool) {
	asked := r.Header.Get("Range")
	sv, refused := s.resume(asked)
	slog.InfoContext(ctx, "stream GET",
		"from", r.RemoteAddr,
		"user_agent", r.UserAgent(),
		"range", asked,
		"status", sv.code,
		"first_byte", sv.start,
	)
	if refused != "" {
		slog.WarnContext(ctx, "a client asked to continue the stream and is being replayed from the beginning instead",
			"from", r.RemoteAddr, "user_agent", r.UserAgent(), "range", asked, "reason", refused)
	}
	if sv.code == http.StatusGone {
		first, end := s.spool.Retained()
		w.Header().Set("X-Castor-Retained-Bytes", fmt.Sprintf("%d-%d", first, end))
		http.Error(w, ErrSpoolExpired.Error(), sv.code)
		return sv, false
	}
	if sv.code == http.StatusRequestedRangeNotSatisfiable {
		if sv.total >= 0 {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", sv.total))
		}
		http.Error(w, "requested range not satisfiable", sv.code)
		return sv, false
	}
	return sv, true
}

func (s *Stream) respond(w http.ResponseWriter, sv served) http.Flusher {
	if sv.code == http.StatusPartialContent {
		total := "*"
		if sv.total >= 0 {
			total = strconv.FormatInt(sv.total, 10)
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%s", sv.start, sv.end, total))
		w.Header().Set("Content-Length", strconv.FormatInt(sv.end-sv.start+1, 10))
	}
	w.WriteHeader(sv.code)
	flusher, _ := w.(http.Flusher)
	if flusher != nil {
		flusher.Flush()
	}
	return flusher
}

func (s *Stream) send(ctx context.Context, w http.ResponseWriter, r *http.Request, sv served, tail io.Reader, flusher http.Flusher) (reachedEOF bool) {
	// remaining is how many bytes this response still owes, and -1 when it owes the rest of the stream.
	remaining := int64(-1)
	if sv.end >= 0 {
		remaining = sv.end - sv.start + 1
	}

	control := http.NewResponseController(w)
	buf := make([]byte, sendChunkSize)
	var sent int64
	lastWrite := time.Now()
	deadlines := true
	for {
		want := buf
		if remaining >= 0 {
			want = buf[:min(int64(len(buf)), remaining)]
		}
		n, readErr := tail.Read(want)
		if n > 0 {
			if deadlines {
				if err := control.SetWriteDeadline(time.Now().Add(s.o.WriteDeadline)); err != nil {
					deadlines = false
					slog.WarnContext(ctx, "stream write deadline unavailable, a client that stops reading will park this goroutine", "error", err)
				}
			}
			if _, err := w.Write(want[:n]); err != nil {
				s.severed(ctx, r, err, sv.start+sent, time.Since(lastWrite))
				return
			}
			sent += int64(n)
			lastWrite = time.Now()
			if remaining >= 0 {
				remaining -= int64(n)
			}
			// A resumed connection is credited with the prefix it already had.
			s.audience.record(sv.start+sent, lastWrite)
			if flusher != nil {
				flusher.Flush()
			}
		}
		if remaining == 0 {
			// The stretch asked for is delivered.
			reachedEOF = sv.total >= 0 && sv.end == sv.total-1
			slog.InfoContext(ctx, "stream range delivered",
				"from", r.RemoteAddr, "bytes_sent", sent, "first_byte", sv.start, "last_byte", sv.end)
			return
		}
		if readErr != nil {
			if readErr == io.EOF {
				reachedEOF = true
				slog.InfoContext(ctx, "stream fully delivered", "from", r.RemoteAddr, "bytes_sent", sent)
			} else {
				slog.InfoContext(ctx, "stream read ended", "from", r.RemoteAddr, "bytes_sent", sent, "error", readErr)
				if errors.Is(readErr, ErrSpoolExpired) {
					panic(http.ErrAbortHandler)
				}
			}
			return
		}
	}
}

// Err is the producer failure retained by this delivery.
func (s *Stream) Err() error { return s.spool.Err() }

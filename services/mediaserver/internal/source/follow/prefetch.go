package follow

import (
	"context"
	"log/slog"
	"slices"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/source/timeline"
)

const (
	// parallel is how many segments are read from the origin at once, since an origin throttles each connection.
	parallel = 4

	// ahead is how much media past the reader's last request is read before it is asked for.
	ahead = 30 * time.Second

	// attempts is how many requests one segment may have in flight, the first and its hedges.
	attempts = 3

	// hedgeFloor and hedgeDefault bound when a slow read is asked for again; recent reads set it in between.
	hedgeFloor   = 3 * time.Second
	hedgeDefault = 8 * time.Second

	// remembered is how many recent reads the hedge deadline is taken from.
	remembered = 8

	// retryAfter is the pause before a failed read is asked again, growing with each failure.
	retryAfter = 250 * time.Millisecond
)

// ring is the last few read durations.
type ring struct {
	buf   [remembered]time.Duration
	count int
}

func (r *ring) add(d time.Duration) {
	r.buf[r.count%remembered] = d
	r.count++
}

// median is the middle of what the ring holds, false when it holds nothing.
func (r *ring) median() (time.Duration, bool) {
	held := slices.Clone(r.buf[:min(r.count, remembered)])
	if len(held) == 0 {
		return 0, false
	}
	slices.Sort(held)
	return held[len(held)/2], true
}

// slot is one segment read ahead of the reader: its bytes once done closes.
type slot struct {
	done chan struct{}
	body []byte
	err  error
}

// fetched is a segment's bytes, decrypted, read ahead when the reader asked for one before it; it reads ahead past it.
func (f *feed) fetched(ctx context.Context, sequence int64, s timeline.Segment) ([]byte, error) {
	f.mu.Lock()
	f.reached = max(f.reached, sequence)
	sl, ok := f.slots[sequence]
	if !ok || sl.err != nil {
		// The reader is waiting on it, so it does not wait for a free slot; a read ahead that failed is asked for afresh.
		sl = f.launch(sequence, s)
	}
	f.pump()
	f.mu.Unlock()

	select {
	case <-sl.done:
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for held := range f.slots {
		// One behind is kept for a reader that retries the segment it just failed to finish.
		if held < sequence-1 {
			delete(f.slots, held)
		}
	}
	if sl.err != nil && f.slots[sequence] == sl {
		delete(f.slots, sequence)
	}
	return sl.body, sl.err
}

// pump starts reads past the reader, playhead order first, until the window is covered or every slot is busy; f.mu is held.
func (f *feed) pump() {
	if f.life.Err() != nil {
		return
	}
	var covered time.Duration
	for next := f.reached + 1; covered < ahead && f.inFlight < parallel; next++ {
		s, listed := f.ledger.Entry(next)
		if !listed {
			return
		}
		covered += s.Duration
		if _, held := f.slots[next]; !held {
			f.launch(next, s)
		}
	}
}

// launch starts reading one segment under the feed's lifetime; f.mu is held.
func (f *feed) launch(sequence int64, s timeline.Segment) *slot {
	sl := &slot{done: make(chan struct{})}
	f.slots[sequence] = sl
	f.inFlight++
	f.work.Go(func() {
		began := time.Now()
		body, err := f.hedged(f.life, s)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.inFlight--
		sl.body, sl.err = body, err
		if err == nil {
			f.took.add(time.Since(began))
		}
		close(sl.done)
		f.pump()
	})
	return sl
}

// hedged reads s, asking again on a fresh connection when a read runs well past recent ones or fails, and keeps the first whole answer.
func (f *feed) hedged(ctx context.Context, s timeline.Segment) ([]byte, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type answer struct {
		body []byte
		err  error
	}
	answers := make(chan answer, attempts)
	asked, running := 0, 0
	ask := func() {
		asked++
		running++
		go func() {
			body, err := f.read(ctx, s)
			answers <- answer{body, err}
		}()
	}
	defer func() {
		cancel()
		for ; running > 0; running-- {
			<-answers
		}
	}()
	ask()
	hedge := time.NewTimer(f.hedgeAfter())
	defer hedge.Stop()
	// retry is the pause after a failure, nil while none is pending.
	var retry <-chan time.Time
	var failure error
	for {
		select {
		case a := <-answers:
			running--
			if a.err == nil {
				if asked > 1 {
					slog.DebugContext(ctx, "a hedged segment read answered", "input", f.name, "uri", s.URI, "asked", asked)
				}
				return a.body, nil
			}
			failure = a.err
			// The origin's final no is the same on every connection.
			if refusal(a.err) != 0 || ctx.Err() != nil {
				return nil, a.err
			}
			if asked < attempts {
				retry = time.After(retryAfter * time.Duration(asked))
			} else if running == 0 {
				return nil, failure
			}
		case <-retry:
			retry = nil
			if asked < attempts {
				ask()
			}
		case <-hedge.C:
			if asked < attempts {
				slog.DebugContext(ctx, "a segment read is running slow; asking again on a fresh connection", "input", f.name, "uri", s.URI, "asked", asked)
				ask()
				hedge.Reset(f.hedgeAfter())
			}
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		}
	}
}

// hedgeAfter is twice the median of recent reads, within its floor and the feed's patience; with none yet, a default.
func (f *feed) hedgeAfter() time.Duration {
	f.mu.Lock()
	median, any := f.took.median()
	f.mu.Unlock()
	if !any {
		return min(hedgeDefault, patience)
	}
	return min(max(2*median, hedgeFloor), patience)
}

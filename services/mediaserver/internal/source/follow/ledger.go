package follow

import (
	"slices"
	"strings"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/source/timeline"
)

// ledger is the only place the republished timeline is decided.
type ledger struct {
	entries []entry
	// next is the sequence number the next appended segment takes.
	next int64
	// dropped counts the seams trimmed off the front, as EXT-X-DISCONTINUITY-SEQUENCE.
	dropped int64
	// retain is the longest window the origin has published.
	retain int
	// published is the segments appended lately, so one a lagging edge lists again is never republished; it outlives the entries by a window.
	published map[identity]bool
	appended  []identity
	periods   map[string]bool
	closed    bool
	start     *timeline.Start
	longest   time.Duration
}

type entry struct {
	timeline.Segment
	sequence int64
}

// breaks is how one window broke the timeline: Restarts where the origin numbered backwards, Gaps where it skipped ahead unannounced.
type breaks struct {
	Restarts, Gaps int
}

// identity is what makes two listings the same segment, whatever number or signature the origin gave it.
type identity struct {
	path   string
	span   timeline.Range
	period string
	start  int64
}

// identify keys a segment by where it sits and what it is, not by the query a CDN re-signs on every reload.
func identify(s timeline.Segment) identity {
	path, _, _ := strings.Cut(s.URI, "?")
	return identity{path, s.Range, s.Place.Period, s.Place.Start}
}

// Merge appends what the window lists past the last segment published.
func (l *ledger) Merge(w timeline.Window) breaks {
	if l.closed {
		return breaks{}
	}
	// A window overlapping what was published continues after its last known segment; a stale one adds nothing.
	fresh := w.Segments
	for i, s := range w.Segments {
		if l.published[identify(s)] {
			fresh = w.Segments[i+1:]
		}
	}

	var merged breaks
	for _, s := range fresh {
		last, published := l.last()
		switch {
		case !published:
			s.Seam = false
		case s.Place.Period != last.Place.Period:
			if l.periods[s.Place.Period] {
				continue
			}
			s.Seam = true
		case s.Place.Start > last.Place.End:
			// A gap the origin declared is a seam it announced, not a loss.
			if !s.Seam {
				merged.Gaps++
			}
			s.Seam = true
		case s.Place.Start < last.Place.End:
			s.Seam = true
			merged.Restarts++
		}
		l.append(s)
	}

	l.retain = max(l.retain, len(w.Segments))
	l.trim()
	l.start = l.rebase(w)
	l.closed = w.Closed
	return merged
}

// Closed reports an origin that published its end: nothing it lists changes any more.
func (l *ledger) Closed() bool { return l.closed }

func (l *ledger) last() (entry, bool) {
	if len(l.entries) == 0 {
		return entry{}, false
	}
	return l.entries[len(l.entries)-1], true
}

func (l *ledger) append(s timeline.Segment) {
	if l.periods == nil {
		l.periods, l.published = map[string]bool{}, map[identity]bool{}
	}
	l.periods[s.Place.Period] = true
	id := identify(s)
	l.published[id] = true
	l.appended = append(l.appended, id)
	l.entries = append(l.entries, entry{Segment: s, sequence: l.next})
	l.next++
	l.longest = max(l.longest, s.Duration)
}

// trim keeps as many entries as the origin's longest window, so a reader lagging the edge never loses one castor dropped first.
func (l *ledger) trim() {
	// A lagging edge lists at most one window, so twice that is far enough back to remember.
	for len(l.appended) > 2*l.retain {
		delete(l.published, l.appended[0])
		l.appended = l.appended[1:]
	}
	// Period memory follows the same two-window history as segment identity.
	clear(l.periods)
	for _, id := range l.appended {
		l.periods[id.period] = true
	}
	over := len(l.entries) - l.retain
	if over <= 0 {
		return
	}
	// Render tags a seam only past the first entry, so a seam's tag leaves when its segment becomes first.
	for _, e := range l.entries[1:min(over+1, len(l.entries))] {
		if e.Seam {
			l.dropped++
		}
	}
	l.entries = slices.Delete(l.entries, 0, over)
}

// rebase moves a start measured from the origin's first segment onto the ledger's.
func (l *ledger) rebase(w timeline.Window) *timeline.Start {
	if w.Start == nil || w.Start.Offset < 0 || len(w.Segments) == 0 {
		return w.Start
	}
	var before time.Duration
	for _, e := range l.entries {
		if identify(e.Segment) == identify(w.Segments[0]) {
			return &timeline.Start{Offset: w.Start.Offset + before, Precise: w.Start.Precise}
		}
		before += e.Duration
	}
	// The origin's first segment was never published here, so its offset measures nothing in this playlist.
	return nil
}

// Entry is the retained segment published under sequence; sequences run unbroken, so it is found by position.
func (l *ledger) Entry(sequence int64) (timeline.Segment, bool) {
	if len(l.entries) == 0 {
		return timeline.Segment{}, false
	}
	i := sequence - l.entries[0].sequence
	if i < 0 || i >= int64(len(l.entries)) {
		return timeline.Segment{}, false
	}
	return l.entries[i].Segment, true
}

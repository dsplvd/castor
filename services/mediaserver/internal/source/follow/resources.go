package follow

import (
	"context"
	"strconv"
	"sync"
)

// registry names each distinct resource a feed serves, the same name every time it is listed.
type registry[T comparable] struct {
	mu   sync.Mutex
	ids  map[T]int
	held map[int]T
	next int
}

func (r *registry[T]) id(v T) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if id, ok := r.ids[v]; ok {
		return id
	}
	if r.ids == nil {
		r.ids = map[T]int{}
		r.held = map[int]T{}
	}
	r.ids[v] = r.next
	r.held[r.next] = v
	r.next++
	return r.ids[v]
}

func (r *registry[T]) lookup(raw string) (T, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id, err := strconv.Atoi(raw)
	if err != nil || id < 0 {
		var zero T
		return zero, false
	}
	v, ok := r.held[id]
	return v, ok
}

// resources holds what every segment of a run shares, init sections and keys, read once however many ask at once.
type resources[K comparable] struct {
	reads   sync.WaitGroup
	flights map[K]*resourceRead
	mu      sync.Mutex
	held    map[K][]byte
	// order is when each was read, so a stream rotating its key every segment keeps only the recent ones.
	order []K
}

// kept is how many init sections and keys a feed holds: far more than one window ever names.
const kept = 64
const resourceBytes = 32 << 20

type resourceRead struct {
	done chan struct{}
	body []byte
	err  error
}

func (c *resources[K]) get(ctx, lifetime context.Context, id K, read func(context.Context) ([]byte, error)) ([]byte, error) {
	c.mu.Lock()
	if b, ok := c.held[id]; ok {
		c.mu.Unlock()
		return b, nil
	}
	pending, ok := c.flights[id]
	if !ok {
		pending = &resourceRead{done: make(chan struct{})}
		if c.flights == nil {
			c.flights = make(map[K]*resourceRead)
		}
		c.flights[id] = pending
		// Register before releasing the mutex: shutdown can join even if the caller
		// disconnects before the shared read begins running.
		c.reads.Go(func() {
			shared, cancel := context.WithTimeout(lifetime, patience)
			defer cancel()
			b, err := read(shared)
			c.mu.Lock()
			defer c.mu.Unlock()
			delete(c.flights, id)
			if err == nil && len(b) <= resourceBytes {
				if c.held == nil {
					c.held = map[K][]byte{}
				}
				c.held[id], c.order = b, append(c.order, id)
				bytes := 0
				for _, value := range c.held {
					bytes += len(value)
				}
				for len(c.order) > kept || bytes > resourceBytes {
					bytes -= len(c.held[c.order[0]])
					delete(c.held, c.order[0])
					c.order = c.order[1:]
				}
			}
			pending.body, pending.err = b, err
			close(pending.done)
		})
	}
	c.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	case <-pending.done:
		return pending.body, pending.err
	}
}

// retain forgets expired names without reusing their IDs: an old URL must never
// resolve to a different encryption key or initialization section.
func (r *registry[T]) retain(used map[T]bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for value, id := range r.ids {
		if !used[value] {
			delete(r.ids, value)
			delete(r.held, id)
		}
	}
}

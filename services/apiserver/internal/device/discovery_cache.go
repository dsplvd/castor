package device

import (
	"maps"
	"time"
)

const seenDeviceTTL = 10 * time.Minute
const maxSeenDevices = 1024

// discoveries owns retention policy only. Directory's mutex protects it together
// with lease identity claims; expiring observations must never release leases.
type discoveries map[string]observation

type observation struct {
	info Info
	at   time.Time
}

func (d discoveries) prune(now time.Time) {
	maps.DeleteFunc(d, func(_ string, seen observation) bool {
		return now.Sub(seen.at) >= seenDeviceTTL
	})
}

func (d discoveries) remember(found []Info) {
	now := time.Now()
	d.prune(now)
	for _, info := range found {
		id := info.id()
		if id == "" {
			continue
		}
		if _, exists := d[id]; !exists && len(d) >= maxSeenDevices {
			oldest := ""
			var at time.Time
			for candidate, seen := range d {
				if oldest == "" || seen.at.Before(at) || seen.at.Equal(at) && candidate < oldest {
					oldest, at = candidate, seen.at
				}
			}
			delete(d, oldest)
		}
		d[id] = observation{info: info, at: now}
	}
}

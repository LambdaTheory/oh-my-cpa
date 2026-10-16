package feishu

import (
	"sync"
	"time"
)

// Dedup defaults. The set is a bounded guard against Feishu re-delivering the
// same event (long-connection reconnects and at-least-once delivery). It is
// deliberately in memory: a redelivery only matters within the short window the
// set covers, so a restart clearing it is harmless.
const (
	dedupCapacity = 512
	dedupTTL      = 10 * time.Minute
)

// dedup is a bounded, concurrency-safe set of recently handled event keys.
//
// Eviction is whichever comes first: a key older than the TTL, or the oldest key
// once the set is full.
type dedup struct {
	mu    sync.Mutex
	cap   int
	ttl   time.Duration
	now   func() time.Time
	seen  map[string]time.Time
	order []string
}

func newDedup(capacity int, ttl time.Duration) *dedup {
	if capacity <= 0 {
		capacity = dedupCapacity
	}
	if ttl <= 0 {
		ttl = dedupTTL
	}
	return &dedup{cap: capacity, ttl: ttl, now: time.Now, seen: make(map[string]time.Time, capacity)}
}

// mark attempts to record key. It returns true when the caller owns the key and
// should proceed, false when the key was already recorded.
func (d *dedup) mark(key string) bool {
	if key == "" {
		// Without a stable key we cannot dedupe; process rather than risk
		// silently dropping a real event.
		return true
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now()
	d.evict(now)
	if _, ok := d.seen[key]; ok {
		return false
	}
	d.seen[key] = now
	d.order = append(d.order, key)
	d.trimLocked()
	return true
}

// forget removes key so it can be marked again. It is used when a reservation made
// by mark cannot be honoured (the bot is shutting down), so replaying the event
// must be allowed.
func (d *dedup) forget(key string) {
	if key == "" {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.seen[key]; !ok {
		return
	}
	delete(d.seen, key)
	for i, k := range d.order {
		if k == key {
			d.order = append(d.order[:i], d.order[i+1:]...)
			break
		}
	}
}

func (d *dedup) evict(now time.Time) {
	kept := d.order[:0]
	for _, key := range d.order {
		if at, ok := d.seen[key]; ok && now.Sub(at) >= d.ttl {
			delete(d.seen, key)
			continue
		}
		kept = append(kept, key)
	}
	d.order = kept
}

func (d *dedup) trimLocked() {
	for len(d.order) > d.cap {
		oldest := d.order[0]
		d.order = d.order[1:]
		delete(d.seen, oldest)
	}
}

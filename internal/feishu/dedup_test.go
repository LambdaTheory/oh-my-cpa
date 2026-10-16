package feishu

import (
	"testing"
	"time"
)

func TestDedupMarksOnceAndForgets(t *testing.T) {
	d := newDedup(4, time.Minute)
	if !d.mark("a") {
		t.Fatal("first mark should own the key")
	}
	if d.mark("a") {
		t.Fatal("second mark must be a duplicate")
	}
	d.forget("a")
	if !d.mark("a") {
		t.Fatal("a forgotten key can be marked again")
	}
}

func TestDedupEmptyKeyAlwaysProcesses(t *testing.T) {
	d := newDedup(4, time.Minute)
	if !d.mark("") || !d.mark("") {
		t.Fatal("an unstably keyed event must always be processed")
	}
}

func TestDedupEnforcesCapacity(t *testing.T) {
	d := newDedup(4, time.Hour)
	for i := 0; i < 20; i++ {
		d.mark(string(rune('a' + i)))
	}
	if len(d.seen) > 4 || len(d.order) > 4 {
		t.Fatalf("capacity not enforced: seen=%d order=%d", len(d.seen), len(d.order))
	}
}

func TestDedupEvictsPastTTL(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	d := newDedup(16, time.Minute)
	d.now = func() time.Time { return now }
	d.mark("a")
	now = now.Add(2 * time.Minute)
	d.mark("b")
	if _, ok := d.seen["a"]; ok {
		t.Fatal("a key past its TTL should be evicted")
	}
	if _, ok := d.seen["b"]; !ok {
		t.Fatal("a fresh key should remain")
	}
}

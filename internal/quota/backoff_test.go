package quota

import (
	"net/http"
	"testing"
	"time"
)

func TestBackoffHonoursRetryAfterThenClears(t *testing.T) {
	backoff := NewBackoff(5*time.Minute, 6*time.Hour)
	now := time.Now()
	backoff.Record("claude", "cl-1", 90*time.Second, true, now)
	blocked, until := backoff.CredentialBlocked("cl-1", now)
	if !blocked || until.Sub(now) != 90*time.Second {
		t.Fatalf("blocked=%v until=%v, want 90s", blocked, until)
	}
	if blocked, _ := backoff.CredentialBlocked("cl-1", now.Add(91*time.Second)); blocked {
		t.Fatal("credential still blocked after its horizon")
	}
	// A provider-level park is visible to the scheduler's combined check.
	if blocked, _ := backoff.ProviderBlocked("claude", now); !blocked {
		t.Fatal("provider not parked after a 429")
	}
	backoff.Clear("claude", "cl-1")
	if blocked, _ := backoff.Blocked("claude", "cl-1", now); blocked {
		t.Fatal("credential still blocked after Clear")
	}
}

func TestBackoffDoublesWithoutRetryAfterAndCaps(t *testing.T) {
	backoff := NewBackoff(5*time.Minute, 20*time.Minute)
	now := time.Now()
	var waits []time.Duration
	for i := 0; i < 4; i++ {
		backoff.Record("codex", "c-1", 0, false, now)
		_, until := backoff.CredentialBlocked("c-1", now)
		waits = append(waits, until.Sub(now))
	}
	want := []time.Duration{5 * time.Minute, 10 * time.Minute, 20 * time.Minute, 20 * time.Minute}
	for i, wait := range waits {
		if wait != want[i] {
			t.Fatalf("wait %d = %v, want %v (all %v)", i, wait, want[i], waits)
		}
	}
}

func TestParseRetryAfterAcceptsSecondsAndDate(t *testing.T) {
	if wait, ok := parseRetryAfter(map[string][]string{"retry-after": {"120"}}); !ok || wait != 120*time.Second {
		t.Fatalf("seconds = %v %v", wait, ok)
	}
	future := time.Now().Add(2 * time.Minute).UTC().Format(http.TimeFormat)
	wait, ok := parseRetryAfter(map[string][]string{"Retry-After": {future}})
	if !ok || wait <= 0 || wait > 2*time.Minute+time.Second {
		t.Fatalf("date = %v %v", wait, ok)
	}
	if _, ok := parseRetryAfter(map[string][]string{"Retry-After": {"not-a-wait"}}); ok {
		t.Fatal("unreadable Retry-After accepted")
	}
	if _, ok := parseRetryAfter(nil); ok {
		t.Fatal("absent Retry-After accepted")
	}
	// A hostile value is bounded rather than trusted.
	if wait, ok := parseRetryAfter(map[string][]string{"Retry-After": {"100000000"}}); !ok || wait != 24*time.Hour {
		t.Fatalf("unbounded Retry-After = %v %v", wait, ok)
	}
}

package feishu

import (
	"strings"
	"testing"
	"time"
)

// TestQueryCardNeverShowsCachedMetadataAsLive pins that a report with no fresh
// reading is labelled as cache or unknown, never as live, so the bot does not
// present cached metadata as realtime.
func TestQueryCardNeverShowsCachedMetadataAsLive(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

	// No reading at all (metadata-only): unknown, never live.
	empty := queryMessage(intent{kind: intentStatus}, Report{}, freshnessOf(Report{}, defaultFreshWindow, now))
	if empty.Freshness != "时间未知" {
		t.Fatalf("a report with no reading must read as time-unknown, got %q", empty.Freshness)
	}

	// An old reading is cache, not live, and carries the staleness notice.
	old := Report{GeneratedAt: now.Add(-2 * time.Hour)}
	msg := queryMessage(intent{kind: intentStatus}, old, freshnessOf(old, defaultFreshWindow, now))
	if !strings.Contains(msg.Freshness, "缓存") || msg.Notice == "" {
		t.Fatalf("an old reading must read as cache with a notice, got %q / %q", msg.Freshness, msg.Notice)
	}

	// A current reading is live.
	live := Report{GeneratedAt: now.Add(-time.Minute)}
	if !freshnessOf(live, defaultFreshWindow, now).live {
		t.Fatal("a reading within the window is live")
	}
}

// TestMetadataFreshTreatsUnknownAgeAsStale pins that a zero metadata sync time is
// unknown, never fresh, so it cannot be current auth-invalid evidence.
func TestMetadataFreshTreatsUnknownAgeAsStale(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	if (AccountReport{MetadataObservedAt: time.Time{}}).MetadataFresh(now, defaultFreshWindow) {
		t.Fatal("an unknown metadata age must never be fresh")
	}
	if !(AccountReport{MetadataObservedAt: now.Add(-time.Minute)}).MetadataFresh(now, defaultFreshWindow) {
		t.Fatal("a recent metadata sync is fresh")
	}
	if (AccountReport{MetadataObservedAt: now.Add(-time.Hour)}).MetadataFresh(now, defaultFreshWindow) {
		t.Fatal("an old metadata sync is not fresh")
	}
}

// TestCredentialMetadataCurrentNeedsAFreshSweep pins the removal rule: a row
// older than the window is stale only when the sweep that contains it is still
// fresh. A stale sweep cannot distinguish a removed credential from one that
// simply was not synced, so it must not hide anything.
func TestCredentialMetadataCurrentNeedsAFreshSweep(t *testing.T) {
	sweep := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		observed time.Time
		sweep    time.Time
		want     bool
	}{
		{"fresh row", sweep.Add(-time.Hour), sweep, true},
		{"just inside the window", sweep.Add(-CredentialMetadataStaleAfter), sweep, true},
		{"outside the window with a fresh sweep", sweep.Add(-CredentialMetadataStaleAfter - time.Second), sweep, false},
		{"unknown row age", time.Time{}, sweep, true},
		{"unknown sweep", sweep.Add(-19 * 24 * time.Hour), time.Time{}, true},
		{"stale sweep keeps its rows", sweep.Add(-19 * 24 * time.Hour), sweep.Add(-19 * 24 * time.Hour), true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := IsCredentialMetadataCurrent(testCase.observed, testCase.sweep); got != testCase.want {
				t.Fatalf("IsCredentialMetadataCurrent(%s, %s) = %v, want %v", testCase.observed, testCase.sweep, got, testCase.want)
			}
		})
	}
}

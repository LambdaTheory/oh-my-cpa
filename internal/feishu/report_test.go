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

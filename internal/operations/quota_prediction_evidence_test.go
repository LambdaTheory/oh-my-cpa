package operations

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
)

func TestPredictionDeliveryBoundMatchesWindowCadenceWithoutChangingPublicJSON(t *testing.T) {
	now := time.Date(2026, 11, 6, 2, 0, 0, 0, time.UTC).UnixMilli()
	for _, test := range []struct {
		hours float64
		bound time.Duration
	}{{5, 2 * time.Hour}, {24, 6 * time.Hour}, {168, 24 * time.Hour}} {
		hours := test.hours
		window := quota.QuotaWindow{PeriodHours: &hours}
		// No exact cycle is supplied, so this projection has no repository reads.
		item := (&Service{}).predictQuotaWindow(context.Background(), Quota{ObservedAtMS: now}, window, "weekly", "weekly", now)
		want := test.bound.Milliseconds()
		if item.Accounting.FreshWindowMS != want || want < (2*time.Hour).Milliseconds() || want > (24*time.Hour).Milliseconds() {
			t.Fatalf("period=%v internal bound=%d want=%d", hours, item.Accounting.FreshWindowMS, want)
		}
		encoded, err := json.Marshal(item)
		if err != nil || strings.Contains(string(encoded), "FreshWindow") || strings.Contains(string(encoded), "fresh_window") {
			t.Fatalf("internal delivery field changed the public allowlist: %s err=%v", encoded, err)
		}
	}
}

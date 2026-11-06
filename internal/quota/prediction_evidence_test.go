package quota

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPredictionDeliveryUsesExistingCadenceWithoutAnAccountingCopy(t *testing.T) {
	if _, exists := reflect.TypeFor[PredictionAccounting]().FieldByName("FreshWindowMS"); exists {
		t.Fatal("delivery must use the existing freshness source, not duplicate accounting metadata")
	}
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	for _, status := range []string{"healthy", "stale", "error"} {
		for _, test := range []struct {
			period float64
			bound  time.Duration
		}{{5, 2 * time.Hour}, {24, 6 * time.Hour}, {168, 24 * time.Hour}} {
			input := PredictionInput{Window: QuotaWindow{PeriodHours: &test.period}, ObservedAtMS: now.UnixMilli(), NowMS: now.UnixMilli(), Status: status}
			bound := PredictionFreshWindow(input.Window).Milliseconds()
			if bound != test.bound.Milliseconds() {
				t.Fatalf("status=%s period=%v bound=%d want=%d", status, test.period, bound, test.bound.Milliseconds())
			}
			encoded, err := json.Marshal(PredictWindow(input).Accounting)
			if err != nil || strings.Contains(string(encoded), "FreshWindow") || strings.Contains(string(encoded), "fresh_window") {
				t.Fatalf("internal delivery bound leaked: %s err=%v", encoded, err)
			}
		}
	}
}

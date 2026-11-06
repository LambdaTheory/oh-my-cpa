package quota

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPredictionAccountingCarriesDeliveryBoundWithoutChangingWireShape(t *testing.T) {
	field, ok := reflect.TypeFor[PredictionAccounting]().FieldByName("FreshWindowMS")
	if !ok || field.Tag.Get("json") != "-" || field.Tag.Get("jsonschema") != "-" {
		t.Fatalf("delivery metadata must not enter JSON or schema: %+v", field)
	}
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	for _, status := range []string{"healthy", "stale", "error"} {
		for _, test := range []struct {
			period float64
			bound  time.Duration
		}{{5, 2 * time.Hour}, {24, 6 * time.Hour}, {168, 24 * time.Hour}} {
			input := PredictionInput{Window: QuotaWindow{PeriodHours: &test.period}, ObservedAtMS: now.UnixMilli(), NowMS: now.UnixMilli(), Status: status}
			accounting := PredictWindow(input).Accounting
			if accounting.FreshWindowMS != test.bound.Milliseconds() {
				t.Fatalf("status=%s period=%v bound=%d want=%d", status, test.period, accounting.FreshWindowMS, test.bound.Milliseconds())
			}
			encoded, err := json.Marshal(accounting)
			if err != nil || strings.Contains(string(encoded), "FreshWindow") || strings.Contains(string(encoded), "fresh_window") {
				t.Fatalf("internal delivery bound leaked: %s err=%v", encoded, err)
			}
		}
	}
}

package quota

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/cpa/management"
)

func percentIs(value *float64, want float64) bool {
	return value != nil && math.Abs(*value-want) < 1e-6
}

const commandCodeCreditsFixture = `{
	"credits": {"belowThreshold": false, "creditThreshold": 10, "monthlyCredits": 42.5, "purchasedCredits": 0},
	"windowLimits": {
		"limited": true,
		"fiveHour": {"used": 3, "cap": 25, "exceeded": false, "resetAt": 1794944016000},
		"weekly": {"used": 28, "cap": 70, "exceeded": false, "resetAt": 1795530816000}
	}
}`

const commandCodeSubscriptionFixture = `{"success":true,"data":{"planId":"individual-goat","status":"active","currentPeriodEnd":"2026-12-06T04:03:37.000Z"}}`

func TestParseCommandCodeQuotaProjectsTheCreditsAndSubscriptionDocuments(t *testing.T) {
	const nowMS int64 = 1794900000000
	plan, windows, err := ParseCommandCodeQuota([]byte(commandCodeCreditsFixture), []byte(commandCodeSubscriptionFixture), nowMS)
	if err != nil {
		t.Fatal(err)
	}
	if plan == nil || plan.PlanType != "individual-goat" || plan.PlanLabel != "GOAT" || plan.Tier != "standard" {
		t.Fatalf("plan = %+v", plan)
	}
	if plan.Credits == nil || plan.Credits.Balance != "42.5" {
		t.Fatalf("credits = %+v", plan.Credits)
	}
	if len(windows) != 3 {
		t.Fatalf("windows = %+v, want five_hour, weekly, monthly", windows)
	}

	fiveHour := windows[0]
	if fiveHour.ID != "five_hour" || fiveHour.Kind != "five_hour" || fiveHour.PeriodHours == nil || *fiveHour.PeriodHours != 5 ||
		!percentIs(fiveHour.UsedPercent, 12) || !percentIs(fiveHour.RemainingPercent, 88) ||
		fiveHour.ResetAtMS == nil || *fiveHour.ResetAtMS != 1794944016000 || fiveHour.ResetAccuracy != "exact" {
		t.Fatalf("five_hour = %+v", fiveHour)
	}
	if fiveHour.Used == nil || *fiveHour.Used != 3 || fiveHour.Limit == nil || *fiveHour.Limit != 25 {
		t.Fatalf("five_hour absolute figures = %+v", fiveHour)
	}

	weekly := windows[1]
	if weekly.ID != "weekly" || weekly.Kind != "weekly" || weekly.PeriodHours == nil || *weekly.PeriodHours != 168 ||
		!percentIs(weekly.UsedPercent, 40) || weekly.ResetAtMS == nil || *weekly.ResetAtMS != 1795530816000 {
		t.Fatalf("weekly = %+v", weekly)
	}

	monthly := windows[2]
	wantReset := time.Date(2026, 12, 6, 4, 3, 37, 0, time.UTC).UnixMilli()
	if monthly.ID != "monthly" || monthly.Kind != "monthly" || monthly.PeriodHours == nil || *monthly.PeriodHours != 720 ||
		!percentIs(monthly.UsedPercent, 27.5/70*100) || !percentIs(monthly.RemainingPercent, 100-27.5/70*100) ||
		monthly.Used == nil || *monthly.Used != 27.5 || monthly.Limit == nil || *monthly.Limit != 70 ||
		monthly.ResetAtMS == nil || *monthly.ResetAtMS != wantReset || monthly.ResetAccuracy != "exact" {
		t.Fatalf("monthly = %+v", monthly)
	}
}

func TestParseCommandCodeQuotaMapsExceededToTheExhaustedReading(t *testing.T) {
	raw := `{"windowLimits":{"fiveHour":{"used":26,"cap":25,"exceeded":true,"resetAt":1794944016000}}}`
	_, windows, err := ParseCommandCodeQuota([]byte(raw), nil, 1794900000000)
	if err != nil || len(windows) != 1 {
		t.Fatalf("windows = %+v, err = %v", windows, err)
	}
	window := windows[0]
	if window.UsedPercent == nil || *window.UsedPercent != 100 || window.RemainingPercent == nil || *window.RemainingPercent != 0 {
		t.Fatalf("exceeded window = %+v", window)
	}
	if !strings.Contains(window.Note, "超额") {
		t.Fatalf("note = %q, want the exceeded explanation", window.Note)
	}
}

func TestParseCommandCodeQuotaKeepsMissingReadingsMissing(t *testing.T) {
	raw := `{"windowLimits":{"fiveHour":{"used":3,"resetAt":0},"weekly":{"used":4,"cap":0,"exceeded":false,"resetAt":-1}}}`
	_, windows, err := ParseCommandCodeQuota([]byte(raw), nil, 1794900000000)
	if err != nil || len(windows) != 2 {
		t.Fatalf("windows = %+v, err = %v", windows, err)
	}
	for _, window := range windows {
		if window.UsedPercent != nil || window.RemainingPercent != nil {
			t.Fatalf("missing denominator became a percentage: %+v", window)
		}
		if window.ResetAtMS != nil || window.ResetAccuracy != "" {
			t.Fatalf("invalid reset became a cycle: %+v", window)
		}
		if !strings.Contains(window.Note, "上限") {
			t.Fatalf("note = %q, want the missing-denominator explanation", window.Note)
		}
	}
}

func TestParseCommandCodeQuotaMarksUnlimitedAccounts(t *testing.T) {
	raw := `{"windowLimits":{"limited":false,"fiveHour":{"used":0,"cap":0,"resetAt":1794944016000}}}`
	_, windows, err := ParseCommandCodeQuota([]byte(raw), nil, 1794900000000)
	if err != nil || len(windows) != 1 {
		t.Fatalf("windows = %+v, err = %v", windows, err)
	}
	if !strings.Contains(windows[0].Note, "不限量") {
		t.Fatalf("note = %q, want the unlimited explanation", windows[0].Note)
	}
}

func TestParseCommandCodeQuotaUnknownPlanGetsNoInventedDenominator(t *testing.T) {
	raw := `{"credits":{"monthlyCredits":12.5},"windowLimits":{"fiveHour":{"used":1,"cap":5,"resetAt":1794944016000}}}`
	subscription := `{"success":true,"data":{"planId":"enterprise-x","currentPeriodEnd":"2026-12-06T04:03:37Z"}}`
	plan, windows, err := ParseCommandCodeQuota([]byte(raw), []byte(subscription), 1794900000000)
	if err != nil {
		t.Fatal(err)
	}
	if plan == nil || plan.PlanType != "enterprise-x" || plan.PlanLabel != "enterprise-x" {
		t.Fatalf("plan = %+v", plan)
	}
	if len(windows) != 2 {
		t.Fatalf("windows = %+v", windows)
	}
	monthly := windows[1]
	if monthly.ID != "monthly" || monthly.UsedPercent != nil || monthly.Limit != nil || monthly.Used != nil {
		t.Fatalf("unknown plan produced a denominator: %+v", monthly)
	}
	if !strings.Contains(monthly.Note, "不在厂商额度表内") || !strings.Contains(monthly.Note, "12.5") {
		t.Fatalf("note = %q, want the plan and remaining credits", monthly.Note)
	}
	if monthly.ResetAtMS == nil {
		t.Fatal("the subscription's currentPeriodEnd was dropped")
	}
}

func TestParseCommandCodeQuotaSubscriptionIsBestEffort(t *testing.T) {
	for name, subscription := range map[string]string{
		"absent":    "",
		"malformed": `not-json`,
		"unsuccess": `{"success":false,"data":{"planId":"individual-goat","currentPeriodEnd":"2026-12-06T04:03:37Z"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			plan, windows, err := ParseCommandCodeQuota([]byte(commandCodeCreditsFixture), []byte(subscription), 1794900000000)
			if err != nil {
				t.Fatal(err)
			}
			if len(windows) != 3 || windows[0].ID != "five_hour" || windows[1].ID != "weekly" {
				t.Fatalf("rate-limit windows lost: %+v", windows)
			}
			monthly := windows[2]
			if monthly.ResetAtMS != nil || monthly.ResetAccuracy != "" {
				t.Fatalf("monthly reset invented without a successful subscription: %+v", monthly)
			}
			if plan == nil || plan.Credits == nil || plan.Credits.Balance != "42.5" {
				t.Fatalf("credits balance lost: %+v", plan)
			}
		})
	}
}

func TestParseCommandCodeQuotaRefusesANonObjectDocument(t *testing.T) {
	for _, raw := range []string{``, `null`, `[]`, `not-json`} {
		if _, _, err := ParseCommandCodeQuota([]byte(raw), nil, 0); err == nil {
			t.Fatalf("invalid credits document accepted: %q", raw)
		}
	}
}

func TestCommandCodeQuotaFetchUsesBothBillingDocuments(t *testing.T) {
	requests := make([]management.ApiCallRequest, 0, 2)
	client := &mockCPAClient{apiCallFunc: func(ctx context.Context, request management.ApiCallRequest) (management.ApiCallResponse, error) {
		requests = append(requests, request)
		switch request.URL {
		case CommandCodeCreditsURL:
			return management.ApiCallResponse{StatusCode: 200, Body: json.RawMessage(commandCodeCreditsFixture)}, nil
		case CommandCodeSubscriptionsURL:
			return management.ApiCallResponse{StatusCode: 200, Body: json.RawMessage(commandCodeSubscriptionFixture)}, nil
		default:
			return management.ApiCallResponse{}, errors.New("unexpected URL")
		}
	}}
	result, err := NewService(client).RefreshCredentialQuota(context.Background(), management.AuthFile{
		AuthIndex: "cc-auth", Name: "commandcode-key-fixture.json", Type: "commandcode", Provider: "commandcode",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 {
		t.Fatalf("requests = %d, want credits then subscriptions", len(requests))
	}
	for _, request := range requests {
		if request.Method != "GET" || request.AuthIndex != "cc-auth" ||
			request.Header["Authorization"] != management.QuotaTokenPlaceholder || request.Header["Accept"] != "application/json" {
			t.Fatalf("unsafe request: %+v", request)
		}
	}
	if requests[0].URL != CommandCodeCreditsURL || requests[1].URL != CommandCodeSubscriptionsURL {
		t.Fatalf("call order = %q, %q", requests[0].URL, requests[1].URL)
	}
	if len(result.Windows) != 3 || !result.Capabilities.RefreshSupported || result.Provider != "commandcode" {
		t.Fatalf("reading = %+v", result)
	}
}

func TestCommandCodeQuotaSubscriptionFailureKeepsTheWindows(t *testing.T) {
	client := &mockCPAClient{apiCallFunc: func(ctx context.Context, request management.ApiCallRequest) (management.ApiCallResponse, error) {
		if request.URL == CommandCodeCreditsURL {
			return management.ApiCallResponse{StatusCode: 200, Body: json.RawMessage(commandCodeCreditsFixture)}, nil
		}
		return management.ApiCallResponse{StatusCode: 500, Body: json.RawMessage(`{"error":{"message":"synthetic-private"}}`)}, nil
	}}
	result, err := NewService(client).RefreshCredentialQuota(context.Background(), management.AuthFile{
		AuthIndex: "cc-auth", Type: "commandcode", Provider: "commandcode",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Windows) < 2 || result.Error != "" {
		t.Fatalf("supplemental failure discarded the reading: %+v", result)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "synthetic-private") {
		t.Fatal("upstream error body leaked into the reading")
	}
}

func TestCommandCodeQuotaRateLimitRetainsTheTypedBackoff(t *testing.T) {
	client := &mockCPAClient{apiCallFunc: func(ctx context.Context, request management.ApiCallRequest) (management.ApiCallResponse, error) {
		return management.ApiCallResponse{
			StatusCode: 429,
			Header:     map[string][]string{"Retry-After": {"120"}},
			Body:       json.RawMessage(`{"error":{"message":"synthetic-private"}}`),
		}, nil
	}}
	used := 20.0
	remaining := 80.0
	prior := &NormalizedQuota{
		AuthIndex:    "cc-auth",
		ObservedAtMS: 1794800000000,
		Windows:      []QuotaWindow{{ID: "weekly", UsedPercent: &used, RemainingPercent: &remaining}},
	}
	result, err := NewService(client).RefreshCredentialQuota(context.Background(), management.AuthFile{
		AuthIndex: "cc-auth", Type: "commandcode", Provider: "commandcode",
	}, prior)
	var rate *RateLimitError
	if !errors.As(err, &rate) || rate.RetryAfter.Seconds() != 120 || rate.Provider != "commandcode" || rate.AuthIndex != "cc-auth" {
		t.Fatalf("err = %v, want the typed backoff", err)
	}
	if result.Status != "stale" || len(result.Windows) != 1 || result.Windows[0].ID != "weekly" {
		t.Fatalf("prior windows not retained: %+v", result)
	}
}

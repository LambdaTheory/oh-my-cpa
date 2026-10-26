package quota

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/cpa/management"
)

func TestStepFunBalanceRefresh(t *testing.T) {
	for _, test := range []struct {
		name, payload, status string
		httpStatus            int
		isTimeout             bool
		wantBalance           float64
		wantPercent           *float64
	}{
		{name: "prepaid", payload: `{"type":"prepaid","balance":75,"total_cash_balance":80,"total_voucher_balance":20,"api_key":"synthetic-private"}`, httpStatus: 200, status: "healthy", wantBalance: 75, wantPercent: floatPtr(75)},
		{name: "postpaid", payload: `{"type":"postpaid","balance":-12.5,"total_cash_balance":80,"total_voucher_balance":20}`, httpStatus: 200, status: "healthy", wantBalance: -12.5},
		{name: "401", payload: `{"error":{"message":"unauthorized"}}`, httpStatus: 401, status: "error"},
		{name: "timeout", isTimeout: true, status: "error"},
		{name: "empty balance", payload: `{"type":"prepaid"}`, httpStatus: 200, status: "error"},
		{name: "zero balance", payload: `{"type":"prepaid","balance":0,"total_cash_balance":80,"total_voucher_balance":20}`, httpStatus: 200, status: "exhausted", wantPercent: floatPtr(0)},
		{name: "zero funding", payload: `{"type":"prepaid","balance":0,"total_cash_balance":0,"total_voucher_balance":0}`, httpStatus: 200, status: "exhausted"},
		{name: "missing funding", payload: `{"type":"prepaid","balance":3}`, httpStatus: 200, status: "healthy", wantBalance: 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			callCount := 0
			client := &mockCPAClient{apiCallFunc: func(ctx context.Context, request management.ApiCallRequest) (management.ApiCallResponse, error) {
				callCount++
				if request.URL != StepFunAccountsURL || request.Method != "GET" || request.AuthIndex != "synthetic-step" ||
					request.Header["Authorization"] != management.QuotaTokenPlaceholder || request.Data != "" {
					t.Fatalf("unexpected request: %+v", request)
				}
				if test.isTimeout {
					return management.ApiCallResponse{}, ctx.Err()
				}
				return management.ApiCallResponse{StatusCode: test.httpStatus, Body: json.RawMessage(test.payload)}, nil
			}}
			ctx := context.Background()
			if test.isTimeout {
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, time.Unix(1, 0))
				defer cancel()
			}
			reading, err := NewService(client).RefreshCredentialQuota(ctx, management.AuthFile{
				Type: "step", AuthIndex: "synthetic-step", Name: "synthetic.json", SupportsQuota: true, QuotaProvider: "ignored-plugin",
			}, nil)
			if err != nil || reading.Status != test.status || reading.Provider != "stepfun" || callCount != 1 || !reading.Capabilities.RefreshSupported {
				t.Fatalf("reading=%+v calls=%d err=%v", reading, callCount, err)
			}
			if len(reading.Windows) != 0 {
				t.Fatalf("money became quota windows: %+v", reading.Windows)
			}
			if test.status == "error" {
				if reading.Error == "" || reading.Plan != nil || reading.HasAuthenticatedRead {
					t.Fatalf("failed read claimed balance: %+v", reading)
				}
				if test.httpStatus == 401 && reading.Recommendation.Action != "reauth" {
					t.Fatalf("lost auth failure: %+v", reading.Recommendation)
				}
				if test.isTimeout && reading.Recommendation.Action == "reauth" {
					t.Fatal("timeout became authentication failure")
				}
				return
			}
			if reading.Plan == nil || reading.Plan.AccountBalance == nil {
				t.Fatal("balance missing")
			}
			balance := reading.Plan.AccountBalance
			if balance.Balance != test.wantBalance || balance.Currency != "CNY" || !reading.HasAuthenticatedRead {
				t.Fatalf("balance=%+v", balance)
			}
			if (balance.RemainingPercent == nil) != (test.wantPercent == nil) ||
				(test.wantPercent != nil && *balance.RemainingPercent != *test.wantPercent) {
				t.Fatalf("wrong estimate: %+v", balance)
			}
			encoded, _ := json.Marshal(reading)
			if strings.Contains(string(encoded), "synthetic-private") {
				t.Fatal("unlisted upstream field escaped")
			}
		})
	}
}

func TestStepFunBalanceValidation(t *testing.T) {
	for _, payload := range []string{`{}`, `null`, `{"type":"other","balance":1}`, `{"type":"prepaid","balance":null}`, `{"type":"prepaid","balance":"synthetic-private"}`, `{"type":"prepaid","balance":1e999}`} {
		plan, err := parseStepFunBalance([]byte(payload))
		if err == nil || plan != nil || strings.Contains(err.Error(), "synthetic-private") {
			t.Fatalf("invalid balance accepted or echoed: plan=%+v err=%v", plan, err)
		}
	}
	for _, payload := range []string{`{"type":"prepaid","balance":1,"total_cash_balance":-1,"total_voucher_balance":2}`, `{"type":"prepaid","balance":1,"total_cash_balance":1}`} {
		plan, err := parseStepFunBalance([]byte(payload))
		if err != nil || plan.AccountBalance.RemainingPercent != nil {
			t.Fatalf("invalid denominator produced estimate: plan=%+v err=%v", plan, err)
		}
	}
}

func TestStepFunProviderAndEndpoint(t *testing.T) {
	for _, provider := range []string{"step", "stepfun", " StepFun "} {
		if DetectProvider(provider, "") != "stepfun" || DetectProvider("", provider) != "stepfun" {
			t.Fatalf("provider not detected: %s", provider)
		}
	}
	if !IsAllowedQuotaURL(StepFunAccountsURL) {
		t.Fatal("accounts endpoint refused")
	}
	for _, target := range []string{StepFunAccountsURL + "/other", StepFunAccountsURL + "-other", "https://api.stepfun.com/v1/chat/completions", "https://api.stepfun.com.attacker.invalid/v1/accounts"} {
		if IsAllowedQuotaURL(target) {
			t.Fatalf("unexpected allowed target: %s", target)
		}
	}
}

func TestStepFunFailedRefreshKeepsObservationTime(t *testing.T) {
	prior := &NormalizedQuota{ObservedAtMS: 1000, Plan: &QuotaPlan{AccountBalance: &QuotaAccountBalance{AccountType: "prepaid", Currency: "CNY", Balance: 75}}}
	client := &mockCPAClient{apiCallFunc: func(context.Context, management.ApiCallRequest) (management.ApiCallResponse, error) {
		return management.ApiCallResponse{}, context.DeadlineExceeded
	}}
	reading, err := NewService(client).RefreshCredentialQuota(context.Background(), management.AuthFile{Type: "step", AuthIndex: "synthetic"}, prior)
	if err != nil || reading.Error == "" || reading.Status != "stale" || reading.ObservedAtMS != prior.ObservedAtMS || reading.HasAuthenticatedRead {
		t.Fatalf("failed refresh claimed a fresh balance: %+v, %v", reading, err)
	}
}

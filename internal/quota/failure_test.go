package quota

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/authfailure"
	"github.com/oh-my-cpa/oh-my-cpa/internal/cpa/management"
)

func TestFetcherPreservesCredentialFailureEvidence(t *testing.T) {
	for _, tc := range []struct {
		provider string
		status   int
		body     string
		want     bool
	}{
		{"claude", 401, `{"error":{"type":"authentication_error","message":"OAuth access token has expired"}}`, true},
		{"codex", 401, `{}`, true},
		{"antigravity", 403, `{"error":{"reason":"payment_required"}}`, false},
		{"kimi", 403, `{"error":{"code":"invalid_token"}}`, true},
		{"devin", 200, `{"code":"unauthenticated","message":"session invalid"}`, true},
		{"claude", 200, `{"error":{"message":"Re-authenticate"}}`, true},
		{"codex", 200, `{}`, false},
		{"claude", 200, `{"unrelated":true}`, false},
		{"claude", 200, `{}`, false},
		{"claude", 200, `{"usage_limits":[]}`, false},
		{"claude", 200, `{"five_hour":null,"seven_day":null}`, false},
		{"claude", 200, `{"seven_day_sonnet":{"utilization":20},"error":{"code":"invalid_token"}}`, true},
	} {
		t.Run(tc.provider+tc.body, func(t *testing.T) {
			client := &mockCPAClient{apiCallFunc: func(_ context.Context, req management.ApiCallRequest) (management.ApiCallResponse, error) {
				return management.ApiCallResponse{StatusCode: tc.status, Body: json.RawMessage(tc.body)}, nil
			}}
			q, err := NewService(client).RefreshCredentialQuota(context.Background(), management.AuthFile{Type: tc.provider, AuthIndex: "synthetic", ProjectID: "synthetic-project"}, nil)
			if err != nil || q.Error == "" || q.HasAuthenticatedRead || (q.Recommendation.Status == "needs_reauth") != tc.want {
				t.Fatalf("q=%+v err=%v", q, err)
			}
			if tc.status != 200 || tc.want {
				if q.Failure == nil || q.Failure.Endpoint == "" || q.Failure.Source != authfailure.UpstreamQuota {
					t.Fatal("missing evidence")
				}
			}
		})
	}
}

func TestClaudeStandaloneObservationsProveAcceptance(t *testing.T) {
	payloads := map[string]string{
		"limits":       `{"limits":[{"percent":20,"scope":{"model":{"display_name":"Fable"}}}]}`,
		"empty limits": `{"limits":[]}`,
		"extra usage":  `{"extra_usage":{"is_enabled":false}}`,
		"balance":      `{"iguana_necktie":{"limit_dollars":10,"used_dollars":2}}`,
	}
	for _, window := range []string{"five_hour", "seven_day", "seven_day_sonnet", "seven_day_opus", "seven_day_oauth_apps", "seven_day_cowork", "iguana_necktie"} {
		payloads[window] = `{"` + window + `":{"utilization":20}}`
	}
	for name, body := range payloads {
		t.Run(name, func(t *testing.T) {
			client := &mockCPAClient{apiCallFunc: func(context.Context, management.ApiCallRequest) (management.ApiCallResponse, error) {
				return management.ApiCallResponse{StatusCode: 200, Body: json.RawMessage(body)}, nil
			}}
			q, err := NewService(client).RefreshCredentialQuota(context.Background(), management.AuthFile{Type: "claude", AuthIndex: "synthetic"}, nil)
			if err != nil || q.Error != "" || !q.HasAuthenticatedRead {
				t.Fatalf("supported observation rejected: error=%v quotaError=%s accepted=%v", err, q.Error, q.HasAuthenticatedRead)
			}
		})
	}
}

func TestPersistedAcceptanceOrdersAccountEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, updatedAt string
		wantAuth        bool
	}{
		{"undated", `null`, false},
		{"older", `"2023-11-14T22:13:19Z"`, false},
		{"same", `1700000000000`, false},
		{"newer", `"2023-11-14T22:13:21Z"`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := &NormalizedQuota{LastAuthenticatedReadAtMS: 1700000000000}
			ApplyCredentialStatus(q, management.AuthFile{Status: "error", StatusMessage: "OAuth access token has expired", UpdatedAt: json.RawMessage(tc.updatedAt)}, 1700000002000)
			EvaluateStatusAndRecommendation(q, 1700000002000)
			if (q.Recommendation.Status == "needs_reauth") != tc.wantAuth || q.HasAuthenticatedRead {
				t.Fatalf("auth=%s freshAcceptance=%v", q.Recommendation.Status, q.HasAuthenticatedRead)
			}
		})
	}
}

func TestRefreshRetainsHistoricalAcceptanceWithoutLiveProof(t *testing.T) {
	for _, payload := range []string{
		`{"five_hour":{"utilization":20}}`,
		`{"limits":[]}`,
	} {
		for _, tc := range []struct {
			name, body string
			status     int
			wantAuth   bool
		}{
			{"payment", `{"error":{"code":"payment_required"}}`, 403, false},
			{"unauthorized", `{}`, 401, true},
			{"invalid token", `{"error":{"code":"invalid_token"}}`, 403, true},
		} {
			t.Run(payload+"/"+tc.name, func(t *testing.T) {
				ctx := context.Background()
				now := time.UnixMilli(1700000000000)
				response := management.ApiCallResponse{StatusCode: 200, Body: json.RawMessage(payload)}
				client := &mockCPAClient{apiCallFunc: func(context.Context, management.ApiCallRequest) (management.ApiCallResponse, error) {
					return response, nil
				}}
				svc := NewService(client)
				svc.now = func() time.Time { return now }
				file := management.AuthFile{Type: "claude", AuthIndex: "synthetic", Status: "error", StatusMessage: "OAuth access token has expired"}
				prior, err := svc.RefreshCredentialQuota(ctx, file, nil)
				if err != nil || !prior.HasAuthenticatedRead || prior.LastAuthenticatedReadAtMS != now.UnixMilli() {
					t.Fatalf("live acceptance=%+v error=%v", prior, err)
				}
				acceptedAt := now.UnixMilli()
				now = now.Add(time.Minute)
				response = management.ApiCallResponse{StatusCode: tc.status, Body: json.RawMessage(tc.body)}
				q, err := svc.RefreshCredentialQuota(ctx, file, prior)
				if err != nil || q.LastAuthenticatedReadAtMS != acceptedAt || q.HasAuthenticatedRead {
					t.Fatalf("historical acceptance=%+v error=%v", q, err)
				}
				if q.Error == "" || q.Failure == nil || (q.Recommendation.Status == "needs_reauth") != tc.wantAuth {
					t.Fatalf("endpoint evidence lost: %+v", q)
				}
			})
		}
	}
}

func TestNonAuthEndpointFailureDoesNotMaskAccountRejection(t *testing.T) {
	for _, source := range []string{"status", "account cooldown", "credential cooldown", "active"} {
		t.Run(source, func(t *testing.T) {
			file := management.AuthFile{Status: "active", Cooldowns: []management.AuthCooldown{{Scope: "model", Reason: "payment_required", HTTPStatus: 403}}}
			if source == "status" {
				file.Status, file.StatusMessage = "error", "OAuth access token has expired"
			} else if source != "active" {
				scope := "account"
				if source == "credential cooldown" {
					scope = "credential"
				}
				file.Cooldowns = append(file.Cooldowns, management.AuthCooldown{Scope: scope, Reason: "invalid_token", HTTPStatus: 403})
			}
			q := &NormalizedQuota{Error: "HTTP 403", Failure: &authfailure.Evidence{Source: authfailure.UpstreamQuota, HTTPStatus: 403, Code: "payment_required"}}
			ApplyCredentialStatus(q, file, 1)
			EvaluateStatusAndRecommendation(q, 1)
			if (q.Recommendation.Status == "needs_reauth") != (source != "active") || q.ActiveCooldown == nil || q.Cooldowns[0].Reason != "payment_required" {
				t.Fatalf("recommendation=%s cooldown=%v", q.Recommendation.Status, q.Cooldowns)
			}
		})
	}
}

func TestControlPlaneRejectionIsNotCredentialRejection(t *testing.T) {
	for _, status := range []int{401, 403} {
		client := &mockCPAClient{apiCallFunc: func(context.Context, management.ApiCallRequest) (management.ApiCallResponse, error) {
			return management.ApiCallResponse{}, &management.HTTPError{StatusCode: status, Body: "invalid_token synthetic-sensitive-value"}
		}}
		q, err := NewService(client).RefreshCredentialQuota(context.Background(), management.AuthFile{Type: "codex", AuthIndex: "synthetic"}, nil)
		if err != nil || q.Recommendation.Status == "needs_reauth" || q.Failure.Source != authfailure.ControlPlane {
			t.Fatalf("q=%+v err=%v", q, err)
		}
	}
}

func TestAntigravityFallbackPreservesPriority(t *testing.T) {
	for _, first := range []int{401, 429} {
		client := &mockCPAClient{apiCallFunc: func(_ context.Context, req management.ApiCallRequest) (management.ApiCallResponse, error) {
			status := 404
			if req.URL == AntigravityQuotaURLDaily {
				status = first
			}
			return management.ApiCallResponse{StatusCode: status, Header: map[string][]string{"Retry-After": {"120"}}, Body: json.RawMessage(`{"error":{"message":"rejected"}}`)}, nil
		}}
		q, err := NewService(client).RefreshCredentialQuota(context.Background(), management.AuthFile{Type: "antigravity", AuthIndex: "synthetic", ProjectID: "synthetic"}, nil)
		if first == 401 && (err != nil || q.Recommendation.Status != "needs_reauth") {
			t.Fatalf("lost auth evidence: %+v %v", q, err)
		}
		if first == 429 {
			var rate *RateLimitError
			if !errors.As(err, &rate) || rate.RetryAfter != 120*time.Second || q.Recommendation.Status == "needs_reauth" {
				t.Fatalf("lost backoff: %v", err)
			}
		}
	}
}

func TestXaiApplicableEndpointFailure(t *testing.T) {
	for _, kind := range []string{"api_key", "oauth"} {
		client := &mockCPAClient{apiCallFunc: func(_ context.Context, req management.ApiCallRequest) (management.ApiCallResponse, error) {
			status := 401
			if req.URL == XaiApiMeURL {
				status = 429
			}
			return management.ApiCallResponse{StatusCode: status, Body: json.RawMessage(`{"error":"rejected"}`)}, nil
		}}
		q, err := NewService(client).RefreshCredentialQuota(context.Background(), management.AuthFile{Type: "xai", AccountType: kind, AuthIndex: "synthetic"}, nil)
		if kind == "oauth" && (err != nil || q.Recommendation.Status != "needs_reauth") {
			t.Fatal("ignored billing rejection")
		}
		if kind == "api_key" && rateLimitFrom(err) == nil {
			t.Fatal("attributed CLI rejection to API key")
		}
	}
}

func TestCooldownScopeAndAuthenticationAreIndependent(t *testing.T) {
	var file management.AuthFile
	if err := json.Unmarshal([]byte(`{"status":"active","cooldowns":[{"scope":"model","model_key":"synthetic-model","reason":"payment_required","http_status":403,"retry_at":"2030-01-01T00:00:00Z"}]}`), &file); err != nil {
		t.Fatal(err)
	}
	for _, hasAuth := range []bool{false, true} {
		q := &NormalizedQuota{}
		if hasAuth {
			q.Error = "HTTP 401"
			q.Failure = &authfailure.Evidence{Source: authfailure.UpstreamQuota, HTTPStatus: 401}
		}
		ApplyCredentialStatus(q, file, 1)
		EvaluateStatusAndRecommendation(q, 1)
		if len(q.Cooldowns) != 1 || q.Cooldowns[0].Scope != "model" || q.Cooldowns[0].RetryAtMS != 1893456000000 || q.Cooldowns[0].HTTPStatus != 403 {
			t.Fatal("cooldown lost")
		}
		if (q.Recommendation.Status == "needs_reauth") != hasAuth {
			t.Fatalf("scope erased outcome: %+v", q)
		}
		if !hasAuth && q.Status != "cooldown" {
			t.Fatal("payment cooldown lost")
		}
	}
}

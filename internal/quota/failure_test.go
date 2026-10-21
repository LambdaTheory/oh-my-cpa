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

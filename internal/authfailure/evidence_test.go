package authfailure

import "testing"

func TestCredentialRejectionMatrix(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source Source
		status int
		body   string
		want   bool
	}{
		{"claude expired", UpstreamQuota, 401, `{"error":{"type":"authentication_error","message":"OAuth access token has expired"}}`, true},
		{"codex unauthorized", UpstreamQuota, 401, `{}`, true},
		{"payment", UpstreamQuota, 403, `{"error":{"code":"payment_required"}}`, false},
		{"scope", UpstreamQuota, 403, `{"error":{"message":"insufficient scope"}}`, false},
		{"forbidden", UpstreamQuota, 403, `{"error":"Forbidden"}`, false},
		{"policy", UpstreamQuota, 403, `{"error":{"reason":"policy"}}`, false},
		{"token", UpstreamQuota, 403, `{"error":{"code":"invalid_token"}}`, true},
		{"google reason", UpstreamQuota, 403, `{"error":{"code":403,"message":"Rejected","details":[{"reason":"invalid_token"}]}}`, true},
		{"google scope", UpstreamQuota, 403, `{"error":{"details":[{"reason":"ACCESS_TOKEN_SCOPE_INSUFFICIENT"}]}}`, false},
		{"connect", UpstreamQuota, 200, `{"code":"unauthenticated","message":"Login required"}`, true},
		{"refresh grant", TokenRefresh, 400, `{"error":"invalid_grant","error_description":"session revoked"}`, true},
		{"code exchange grant", UpstreamQuota, 400, `{"error":"invalid_grant"}`, false},
		{"client", TokenRefresh, 401, `{"error":"invalid_client"}`, false},
		{"unauthorized client", TokenRefresh, 401, `{"error":"unauthorized_client"}`, false},
		{"management 401", ControlPlane, 401, `{"error":"invalid_token"}`, false},
		{"management 403", ControlPlane, 403, `{"error":"invalid_token"}`, false},
		{"supplemental", Supplemental, 401, `{"error":"invalid_token"}`, false},
		{"rate limit", UpstreamQuota, 429, `{"error":"invalid_token"}`, false},
		{"server failure", UpstreamQuota, 503, `{"error":"invalid_token"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := Parse(tc.source, "/synthetic", tc.status, []byte(tc.body))
			if e.NeedsReauth() != tc.want {
				t.Fatalf("reauth=%v want %v", e.NeedsReauth(), tc.want)
			}
		})
	}
}

func TestLegacyEvidenceAndSafeError(t *testing.T) {
	for _, text := range []string{"OAuth access token has expired", "token has expired", "Re-authenticate", "HTTP 401", "refresh token: invalid_grant"} {
		if !FromText(text).NeedsReauth() {
			t.Errorf("missed %q", text)
		}
	}
	for _, text := range []string{"HTTP 403 forbidden", "CPA returned HTTP 401", "CPA returned HTTP 403 invalid_token", "invalid_grant", "HTTP 401 invalid_client", "HTTP 401 unauthorized_client", "HTTP 401 invalid_grant", "40100 bytes read before timeout", "request id 14013", "token expired network timeout"} {
		if FromText(text).NeedsReauth() {
			t.Errorf("false positive %q", text)
		}
	}
	e := Parse(UpstreamQuota, "/synthetic", 403, []byte(`{"error":{"code":"invalid_token","type":"authentication_error","message":"synthetic-sensitive-value","error_description":"expired","reason":"revoked"}}`))
	if e.Code != "invalid_token" || e.Type != "authentication_error" || e.Message != "synthetic-sensitive-value" || e.Description != "expired" || e.Reason != "revoked" || e.Endpoint != "/synthetic" {
		t.Fatal("evidence lost")
	}
	if e.Error() != "HTTP 403" {
		t.Fatal("unsafe display")
	}
}

func TestStructuredRejectionPrecedesIncidentalProse(t *testing.T) {
	for _, marker := range []string{"quota", "scope", "policy"} {
		for _, field := range []string{"code", "type", "reason"} {
			e := Parse(UpstreamQuota, "/synthetic", 403, []byte(`{"error":{"`+field+`":"invalid_token","message":"`+marker+` request authentication failed"}}`))
			if !e.NeedsReauth() {
				t.Errorf("%s invalid_token vetoed by %s", field, marker)
			}
		}
		e := &Evidence{Source: UpstreamQuota, HTTPStatus: 403, Type: "authentication_error", Message: marker + " request rejected"}
		if !e.NeedsReauth() {
			t.Errorf("authentication_error vetoed by %s", marker)
		}
	}
	for _, code := range []string{"payment_required", "quota_exceeded", "insufficient_scope", "policy", "entitlement", "risk", "challenge", "invalid_client", "unauthorized_client", "invalid_grant"} {
		e := &Evidence{Source: UpstreamQuota, HTTPStatus: 403, Code: code, Type: "authentication_error", Message: "invalid_token"}
		if e.NeedsReauth() {
			t.Errorf("structured non-credential rejection %s admitted", code)
		}
	}
}

package api

import (
	"context"
	"encoding/json"
	"github.com/oh-my-cpa/oh-my-cpa/internal/domain"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
	"github.com/oh-my-cpa/oh-my-cpa/internal/reauth"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

func TestReauthRoutesAuthenticationNetworkAndCodeBoundary(t *testing.T) {
	h := testHandler(t, "/omc")
	ctx := context.Background()
	h.cfg.Feishu.ReauthRunnerCIDRs = []string{"127.0.0.0/8", "172.18.0.0/16"}
	h.reauth = reauth.New(reauth.Options{Repo: h.repo, Enabled: func() bool { return true }, Notify: func(context.Context, repository.ReauthJob, string) error { return nil }})
	h.recordQuotaSignal(ctx, &quota.NormalizedQuota{AuthIndex: "synthetic-auth", Provider: "claude", Name: "Synthetic account", Recommendation: quota.QuotaRecommendation{Status: "needs_reauth"}})
	j, err := h.reauth.Next(ctx, "claude")
	if err != nil {
		t.Fatal(err)
	}
	cookieWriter := httptest.NewRecorder()
	if err := h.auth.Issue(cookieWriter); err != nil {
		t.Fatal(err)
	}
	router := h.Router()
	call := func(method, path, body, peer string, session bool, forwarded string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/omc/api/v1/management/reauth/jobs"+path, strings.NewReader(body))
		req.RemoteAddr = peer
		if session {
			req.AddCookie(cookieWriter.Result().Cookies()[0])
		}
		if forwarded != "" {
			req.Header.Set("X-Forwarded-For", forwarded)
		}
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		return res
	}
	for _, c := range []struct {
		peer      string
		session   bool
		forwarded string
		want      int
	}{
		{"127.0.0.1:4321", false, "", 401}, {"8.8.8.8:4321", true, "", 403}, {"10.2.3.4:4321", true, "", 403},
		{"127.0.0.1:4321", true, "8.8.8.8", 403}, {"172.18.0.3:4321", true, "", 200},
	} {
		res := call("GET", "/next?provider=claude", "", c.peer, c.session, c.forwarded)
		if res.Code != c.want {
			t.Fatalf("peer %s status=%d want=%d body=%s", c.peer, res.Code, c.want, res.Body.String())
		}
	}
	if err := h.reauth.Report(ctx, j.ID, "oauth_started", "", reauth.OAuthStart{}); err != nil {
		t.Fatal(err)
	}
	if err := h.reauth.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if err := h.reauth.AcceptCode(ctx, "synthetic-message", "123456"); err != nil {
		t.Fatal(err)
	}
	res := call("GET", "/"+j.ID, "", "127.0.0.1:4321", true, "")
	var payload map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload) != 11 || strings.Contains(res.Body.String(), "123456") || payload["state"] != "code_received" {
		t.Fatalf("unsafe job DTO: %s", res.Body.String())
	}
	res = call("GET", "/"+j.ID+"/code", "", "127.0.0.1:4321", true, "")
	if res.Code != 200 || !strings.Contains(res.Body.String(), "123456") || res.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("code response invalid")
	}
	res = call("GET", "/"+j.ID+"/code", "", "127.0.0.1:4321", true, "")
	if res.Code != 204 {
		t.Fatal("code was not consumed")
	}
	res = call("POST", "/"+j.ID+"/status", `{"status":"failed","last_error":"123456"}`, "127.0.0.1:4321", true, "")
	if res.Code != 409 || strings.Contains(res.Body.String(), "123456") {
		t.Fatal("unsafe failure accepted")
	}
	for _, status := range []string{"code_submitted", "succeeded"} {
		res = call("POST", "/"+j.ID+"/status", `{"status":"`+status+`"}`, "127.0.0.1:4321", true, "")
		if res.Code != 200 {
			t.Fatalf("status %s: %s", status, res.Body.String())
		}
	}
}

func TestQuotaSignalCreatesOnlyOAuthAuthInvalidJobs(t *testing.T) {
	h := testHandler(t, "/omc")
	h.reauth = reauth.New(reauth.Options{Repo: h.repo, Enabled: func() bool { return true }})
	for _, c := range []struct{ provider, fileType, authIndex, status, want string }{
		{"claude", "", "transport", "", ""},
		{"claude", "", "claude", "needs_reauth", "claude"},
		{"anthropic", "", "alias", "needs_reauth", "claude"},
		{"codex", "", "codex", "needs_reauth", "codex"},
		{"antigravity", "", "antigravity", "needs_reauth", "antigravity"},
		{"kimi", "", "kimi", "needs_reauth", "kimi"},
		{"kimi", "kimi-ai", "international", "needs_reauth", "kimi-ai"},
		{"xai", "", "xai", "needs_reauth", "xai"},
		{"meta", "", "meta", "needs_reauth", "meta"},
		{"devin", "", "devin", "needs_reauth", "devin"},
		{"vertex", "", "vertex", "needs_reauth", ""},
		{"gemini", "", "gemini", "needs_reauth", ""},
		{"unknown", "", "unknown", "needs_reauth", ""},
		{"api-keys.claude", "", "key", "needs_reauth", ""},
		{"claude", "api-keys.claude", "key-type", "needs_reauth", ""},
		{"claude", "", "api-keys.claude.0", "needs_reauth", "claude"},
	} {
		for range 2 {
			h.recordQuotaSignal(context.Background(), &quota.NormalizedQuota{AuthIndex: c.authIndex, Type: c.fileType, Provider: c.provider, Recommendation: quota.QuotaRecommendation{Status: c.status}})
		}
		var count int
		if err := h.repo.SQL().QueryRow("SELECT COUNT(*) FROM reauth_jobs WHERE auth_index=?", c.authIndex).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if c.want == "" {
			if count != 0 {
				t.Fatalf("non-OAuth job: %+v", c)
			}
			continue
		}
		var provider string
		if err := h.repo.SQL().QueryRow("SELECT provider FROM reauth_jobs WHERE auth_index=?", c.authIndex).Scan(&provider); err != nil {
			t.Fatal(err)
		}
		if count != 1 || provider != c.want {
			t.Fatalf("job for %+v: count=%d provider=%s", c, count, provider)
		}
	}
}

func TestReauthRefreshUsesCredentialProviderAndCreatesJobOn401(t *testing.T) {
	for _, provider := range []string{"claude", "codex"} {
		t.Run(provider, func(t *testing.T) {
			h := testHandler(t, "/omc")
			ctx := context.Background()
			isRecovered := false
			cpa := newFakeCPA(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v8/management/credentials":
					writeJSON(w, 200, map[string]any{"files": []map[string]any{{"id": "synthetic-file", "name": "synthetic-claude.json", "auth_index": "synthetic-auth", "type": provider, "provider": provider}}})
				case "/v8/management/requests/api-call":
					var input struct {
						URL string `json:"url"`
					}
					_ = json.NewDecoder(r.Body).Decode(&input)
					if provider == "claude" && !strings.HasPrefix(input.URL, "https://api.anthropic.com/api/oauth/") || provider == "codex" && input.URL != quota.CodexUsageURL && input.URL != quota.CodexResetCreditsURL {
						t.Errorf("unexpected outbound target")
						w.WriteHeader(400)
						return
					}
					if !isRecovered {
						writeJSON(w, 200, map[string]any{"status_code": 401, "body": map[string]any{"error": "unauthorized"}})
						return
					}
					if provider == "codex" {
						writeJSON(w, 200, map[string]any{"status_code": 200, "body": map[string]any{"plan_type": "plus", "rate_limit": map[string]any{"primary_window": map[string]any{"used_percent": 20, "limit_window_seconds": 18000, "reset_after_seconds": 3600}}}})
						return
					}
					writeJSON(w, 200, map[string]any{"status_code": 200, "body": map[string]any{"five_hour": map[string]any{"utilization": 20}, "seven_day": map[string]any{"utilization": 30}}})
				default:
					w.WriteHeader(404)
				}
			}))
			defer cpa.Close()
			ciphertext, nonce, err := h.cipher.Encrypt([]byte("synthetic-management-key"))
			if err != nil {
				t.Fatal(err)
			}
			if err := h.repo.UpsertInstance(ctx, domain.CPAInstance{ID: defaultInstanceID(), Name: "Synthetic CPA", BaseURL: cpa.URL, ManagementKeyCiphertext: ciphertext, ManagementKeyNonce: nonce, CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
				t.Fatal(err)
			}
			h.reauth = reauth.New(reauth.Options{Repo: h.repo, Enabled: func() bool { return true }})
			for range 2 {
				state, err := h.refreshReauthCredential(ctx, "synthetic-auth")
				if err != nil || state != "auth_invalid" {
					t.Fatalf("refresh state=%s err=%v", state, err)
				}
			}
			var count int
			_ = h.repo.SQL().QueryRow(`SELECT COUNT(*) FROM reauth_jobs`).Scan(&count)
			if count != 1 {
				t.Fatalf("jobs=%d", count)
			}
			isRecovered = true
			state, err := h.refreshReauthCredential(ctx, "synthetic-auth")
			if err != nil || state != "healthy" {
				t.Fatalf("recovery state=%s err=%v", state, err)
			}
		})
	}
}

func TestReauthAccountTypeAdmission(t *testing.T) {
	for _, accountType := range []string{"api_key", "API_KEY", "service_account", "unknown", "oauth", ""} {
		for _, path := range []string{"quota_signal", "reauth_refresh"} {
			t.Run(accountType+"/"+path, func(t *testing.T) {
				h := testHandler(t, "/omc")
				ctx := context.Background()
				const cpaIndex = "0123456789abcdef"
				var calls atomic.Int32
				cpa := newFakeCPA(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/v8/management/credentials":
						writeJSON(w, 200, map[string]any{"files": []map[string]any{{"id": "synthetic-file", "name": "synthetic-codex.json", "auth_index": cpaIndex, "type": "codex", "provider": "codex", "account_type": accountType}}})
					case "/v8/management/requests/api-call":
						calls.Add(1)
						writeJSON(w, 200, map[string]any{"status_code": 401, "body": map[string]any{"error": "unauthorized"}})
					default:
						w.WriteHeader(404)
					}
				}))
				defer cpa.Close()
				ciphertext, nonce, err := h.cipher.Encrypt([]byte("synthetic-management-key"))
				if err != nil {
					t.Fatal(err)
				}
				if err := h.repo.UpsertInstance(ctx, domain.CPAInstance{ID: defaultInstanceID(), Name: "Synthetic CPA", BaseURL: cpa.URL, ManagementKeyCiphertext: ciphertext, ManagementKeyNonce: nonce, CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
					t.Fatal(err)
				}
				h.reauth = reauth.New(reauth.Options{Repo: h.repo, Enabled: func() bool { return true }})
				isOAuth := accountType == "oauth" || accountType == ""
				if path == "quota_signal" {
					client, err := h.capabilityClient(ctx)
					if err != nil {
						t.Fatal(err)
					}
					files, err := client.AuthFiles(ctx)
					if err != nil || len(files.Files) != 1 {
						t.Fatalf("credentials=%+v err=%v", files, err)
					}
					value, err := h.readCredentialQuota(ctx, quota.NewService(client), files.Files[0], nil)
					if err != nil || classifyQuotaSignal(value) != "auth_invalid" || calls.Load() == 0 {
						t.Fatalf("quota=%+v calls=%d err=%v", value, calls.Load(), err)
					}
					if err := h.persistNormalizedQuotaSnapshot(ctx, value); err != nil {
						t.Fatal(err)
					}
				} else {
					state, err := h.refreshReauthCredential(ctx, cpaIndex)
					if isOAuth {
						if err != nil || state != "auth_invalid" || calls.Load() == 0 {
							t.Fatalf("refresh state=%s calls=%d err=%v", state, calls.Load(), err)
						}
					} else if err == nil || state != "unknown" || calls.Load() != 0 {
						t.Fatalf("non-OAuth refresh state=%s calls=%d err=%v", state, calls.Load(), err)
					}
				}
				var count int
				if err := h.repo.SQL().QueryRow(`SELECT COUNT(*) FROM reauth_jobs`).Scan(&count); err != nil {
					t.Fatal(err)
				}
				want := 0
				if isOAuth {
					want = 1
				}
				if count != want {
					t.Fatalf("jobs=%d want=%d", count, want)
				}
			})
		}
	}
}

func TestReauthRunnerProviderFlows(t *testing.T) {
	for _, provider := range []string{"claude", "codex", "antigravity", "devin", "kimi", "kimi-ai", "xai", "meta"} {
		t.Run(provider, func(t *testing.T) {
			h := testHandler(t, "/omc")
			ctx := context.Background()
			h.cfg.Feishu.ReauthRunnerCIDRs = []string{"127.0.0.0/8"}
			h.reauth = reauth.New(reauth.Options{Repo: h.repo, Enabled: func() bool { return true }, Notify: func(context.Context, repository.ReauthJob, string) error { return nil }})
			if err := h.reauth.Observe(ctx, "account", provider, "oauth", "", "auth_invalid"); err != nil {
				t.Fatal(err)
			}
			cookie := httptest.NewRecorder()
			if err := h.auth.Issue(cookie); err != nil {
				t.Fatal(err)
			}
			router := h.Router()
			call := func(method, path, body string) *httptest.ResponseRecorder {
				req := httptest.NewRequest(method, "/omc/api/v1/management/reauth/jobs"+path, strings.NewReader(body))
				req.RemoteAddr = "127.0.0.1:4321"
				req.AddCookie(cookie.Result().Cookies()[0])
				res := httptest.NewRecorder()
				router.ServeHTTP(res, req)
				return res
			}
			for _, bad := range []string{"vertex", "gemini", "api-keys.claude", "unknown", ""} {
				if res := call("GET", "/next?provider="+bad, ""); res.Code != 400 {
					t.Fatalf("provider %s=%d", bad, res.Code)
				}
			}
			res := call("GET", "/next?provider="+provider, "")
			var j reauthJobDTO
			if err := json.Unmarshal(res.Body.Bytes(), &j); err != nil || res.Code != 200 {
				t.Fatalf("next=%s err=%v", res.Body, err)
			}
			if j.Provider != provider || j.Flow == "" || j.CodeKind == "" {
				t.Fatalf("DTO=%+v", j)
			}
			body := map[string]any{"status": "oauth_started", "flow": "redirect"}
			if j.CodeKind == "device" {
				body["flow"] = "device"
				body["user_code"] = "ABCD-1234"
				body["verification_url"] = "https://verify.example.test/device"
				body["expires_in"] = 300
			}
			encoded, _ := json.Marshal(body)
			res = call("POST", "/"+j.ID+"/status", string(encoded))
			if res.Code != 200 {
				t.Fatalf("start=%s", res.Body)
			}
			if j.CodeKind == "device" && (!strings.Contains(res.Body.String(), "ABCD-1234") || !strings.Contains(res.Body.String(), "expires_at_ms")) {
				t.Fatalf("device DTO=%s", res.Body)
			}
			if err := h.reauth.Tick(ctx); err != nil {
				t.Fatal(err)
			}
			if err := h.reauth.AcceptCode(ctx, "message", "123456"); err != nil {
				t.Fatal(err)
			}
			res = call("GET", "/"+j.ID+"/code", "")
			if j.CodeKind == "otp" {
				if res.Code != 200 {
					t.Fatalf("OTP=%d", res.Code)
				}
				res = call("POST", "/"+j.ID+"/status", string(mustReauthJSON(t, map[string]any{"status": "code_submitted"})))
				if res.Code != 200 {
					t.Fatal(res.Body)
				}
			} else if res.Code != 204 {
				t.Fatalf("non-OTP code=%d", res.Code)
			}
			res = call("POST", "/"+j.ID+"/status", string(mustReauthJSON(t, map[string]any{"status": "succeeded"})))
			if res.Code != 200 || !strings.Contains(res.Body.String(), "verifying") {
				t.Fatalf("success=%s", res.Body)
			}
		})
	}
}

func mustReauthJSON(t *testing.T, value any) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

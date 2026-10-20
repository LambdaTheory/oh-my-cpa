package api

import (
	"context"
	"encoding/json"
	"github.com/oh-my-cpa/oh-my-cpa/internal/domain"
	"net/http"
	"net/http/httptest"
	"strings"
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
	j, err := h.reauth.Next(ctx)
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
	if err := h.reauth.Report(ctx, j.ID, "oauth_started", ""); err != nil {
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
	if len(payload) != 9 || strings.Contains(res.Body.String(), "123456") || payload["state"] != "code_received" {
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

func TestQuotaSignalCreatesOnlyClaudeAuthInvalidJobs(t *testing.T) {
	h := testHandler(t, "/omc")
	h.reauth = reauth.New(reauth.Options{Repo: h.repo, Enabled: func() bool { return true }})
	for i, q := range []*quota.NormalizedQuota{
		{AuthIndex: "transport", Provider: "claude", Status: "error", Error: "synthetic transport failure"},
		{AuthIndex: "unknown", Provider: "claude", Status: "idle"},
		{AuthIndex: "codex", Provider: "codex", Recommendation: quota.QuotaRecommendation{Status: "needs_reauth"}},
		{AuthIndex: "claude", Name: "Synthetic Claude", Provider: "claude", Recommendation: quota.QuotaRecommendation{Status: "needs_reauth"}},
	} {
		for range 2 {
			h.recordQuotaSignal(context.Background(), q)
		}
		var count int
		_ = h.repo.SQL().QueryRow(`SELECT COUNT(*) FROM reauth_jobs`).Scan(&count)
		want := 0
		if i == 3 {
			want = 1
		}
		if count != want {
			t.Fatalf("case %d jobs=%d", i, count)
		}
	}
}

func TestReauthRefreshUsesClaudeQuotaAndCreatesJobOn401(t *testing.T) {
	h := testHandler(t, "/omc")
	ctx := context.Background()
	isRecovered := false
	cpa := newFakeCPA(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v8/management/credentials":
			writeJSON(w, 200, map[string]any{"files": []map[string]any{{"id": "synthetic-file", "name": "synthetic-claude.json", "auth_index": "synthetic-auth", "type": "claude", "provider": "claude"}}})
		case "/v8/management/requests/api-call":
			var input struct {
				URL string `json:"url"`
			}
			_ = json.NewDecoder(r.Body).Decode(&input)
			if !strings.HasPrefix(input.URL, "https://api.anthropic.com/api/oauth/") {
				t.Errorf("unexpected outbound target")
				w.WriteHeader(400)
				return
			}
			if !isRecovered {
				writeJSON(w, 200, map[string]any{"status_code": 401, "body": map[string]any{"error": "unauthorized"}})
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
}

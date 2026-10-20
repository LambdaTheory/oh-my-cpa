package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/capability"
	"github.com/oh-my-cpa/oh-my-cpa/internal/crypto"
	"github.com/oh-my-cpa/oh-my-cpa/internal/domain"
)

// TestManualRefreshHonoursAndRecordsRateLimitBackoff drives the console refresh
// path against an upstream that answers 429 with a Retry-After, and proves the
// second attempt is refused without reaching the upstream again.
func TestManualRefreshHonoursAndRecordsRateLimitBackoff(t *testing.T) {
	const managementKey = "management-secret"
	var apiCalls int64
	var isRateLimited atomic.Bool
	isRateLimited.Store(true)

	cpaServer := newFakeCPA(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+managementKey {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/v8/management/credentials":
			_, _ = writer.Write([]byte(`{"files":[{"id":"file-1","name":"codex-main.json","auth_index":"codex-idx-1","type":"codex","provider":"codex","disabled":false}]}`))
		case "/v8/management/requests/api-call":
			atomic.AddInt64(&apiCalls, 1)
			if isRateLimited.Load() {
				_, _ = writer.Write([]byte(`{"status_code":429,"header":{"Retry-After":["120"]},"body":{"message":"slow down"}}`))
				return
			}
			_, _ = writer.Write([]byte(`{"status_code":200,"body":{"plan_type":"pro","rate_limit":{"primary_window":{"used_percent":3,"limit_window_seconds":18000,"reset_after_seconds":18000}}}}`))
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer cpaServer.Close()

	handler := testHandler(t, "")
	appServer := httptest.NewServer(handler.Router())
	defer appServer.Close()

	cipher, err := crypto.New("01234567890123456789012345678901")
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, nonce, err := cipher.Encrypt([]byte(managementKey))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	handler.nowFn = func() time.Time { return now }
	if err := handler.repo.UpsertInstance(context.Background(), domain.CPAInstance{
		ID: "default", Name: "Default", BaseURL: cpaServer.URL,
		UsageAddr:               strings.TrimPrefix(cpaServer.URL, "http://"),
		ManagementKeyCiphertext: ciphertext, ManagementKeyNonce: nonce,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	login, err := client.Post(appServer.URL+"/api/auth/login", "application/json", bytes.NewBufferString(`{"password":"management-secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	login.Body.Close()

	post := func() (int, []byte) {
		response, err := client.Post(appServer.URL+"/api/v1/management/quota/refresh", "application/json", bytes.NewBufferString(`{"auth_index":"codex-idx-1"}`))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		return response.StatusCode, body
	}

	status, body := post()
	if status != http.StatusTooManyRequests || !strings.Contains(string(body), "rate_limited_backoff") {
		t.Fatalf("first refresh = %d %s, want a rate_limited_backoff 429", status, body)
	}
	if atomic.LoadInt64(&apiCalls) != 1 {
		t.Fatalf("upstream api-call count = %d, want 1", apiCalls)
	}

	status, body = post()
	if status != http.StatusTooManyRequests || !strings.Contains(string(body), "rate_limited_backoff") {
		t.Fatalf("second refresh = %d %s, want the backoff refusal", status, body)
	}
	if atomic.LoadInt64(&apiCalls) != 1 {
		t.Fatalf("a parked credential reached the upstream again: api-call count = %d", apiCalls)
	}
	var refusal struct {
		RetryAtMS int64 `json:"retry_at_ms"`
	}
	if err := json.Unmarshal(body, &refusal); err != nil || refusal.RetryAtMS <= time.Now().UnixMilli() {
		t.Fatalf("backoff refusal carried no retry time: %s (%v)", body, err)
	}

	now = now.Add(121 * time.Second)
	isRateLimited.Store(false)
	status, body = post()
	if status != http.StatusOK {
		t.Fatalf("successful retry = %d %s", status, body)
	}
	if blocked, _ := handler.quotaBackoffBlocked("codex", "codex-idx-1"); blocked {
		t.Fatal("successful manual refresh did not clear the park")
	}
}

func TestManualBatchRefreshReportsRateLimitBesideSuccessfulResults(t *testing.T) {
	fixture := &quotaAutoRefreshFixture{
		reads: map[string]int{},
		filesJSON: `{"files":[
			{"id":"codex","name":"Codex limited","auth_index":"codex-reset","type":"codex","provider":"codex"},
			{"id":"kimi","name":"Kimi ready","auth_index":"kimi-ready","type":"kimi","provider":"kimi"}
		]}`,
		isRateLimited: true, rateLimitedAuthIndex: "codex-reset", retryAfter: 120,
	}
	handler := newQuotaAutoRefreshHandler(t, fixture)
	now := time.Now().UTC()
	handler.nowFn = func() time.Time { return now }

	appServer := httptest.NewServer(handler.Router())
	defer appServer.Close()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	login, err := client.Post(appServer.URL+"/api/auth/login", "application/json", bytes.NewBufferString(`{"password":"management-secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	login.Body.Close()

	for attempt := 1; attempt <= 2; attempt++ {
		response, err := client.Post(appServer.URL+"/api/v1/management/quota/refresh", "application/json", bytes.NewBufferString(`{"auth_indexes":["codex-reset","kimi-ready"]}`))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("batch refresh %d = %d %s, want 200", attempt, response.StatusCode, body)
		}
		if got := fixture.usageReads("codex-reset"); got != 1 {
			t.Fatalf("batch refresh %d rate-limited upstream reads = %d, want 1", attempt, got)
		}
		var result struct {
			Quotas []struct {
				AuthIndex string `json:"auth_index"`
			} `json:"quotas"`
			Results []struct {
				AuthIndex string `json:"auth_index"`
				Outcome   string `json:"outcome"`
				RetryAtMS int64  `json:"retry_at_ms"`
			} `json:"results"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			t.Fatalf("decode batch %d: %v (%s)", attempt, err, body)
		}
		if len(result.Quotas) != 1 || result.Quotas[0].AuthIndex != "kimi-ready" {
			t.Fatalf("batch %d successful quotas = %+v, want Kimi reading retained", attempt, result.Quotas)
		}
		if len(result.Results) != 2 || result.Results[0].Outcome != "rate_limited" || result.Results[0].RetryAtMS <= now.UnixMilli() || result.Results[1].Outcome != "success" {
			t.Fatalf("batch %d results = %+v, want rate-limited then success", attempt, result.Results)
		}
	}
}

func TestAgentQuotaRefreshRefusesASharedBackoff(t *testing.T) {
	fixture := &quotaAutoRefreshFixture{reads: map[string]int{}}
	handler := newQuotaAutoRefreshHandler(t, fixture)
	now := time.Now().UTC()
	handler.nowFn = func() time.Time { return now }
	handler.quotaBackoff.Record("codex", "codex-reset", 2*time.Minute, true, now)
	if err := handler.ensureAgent(); err != nil {
		t.Fatal(err)
	}
	result, err := handler.agent.executor.Invoke(context.Background(), capability.Principal{ID: "administrator", Adapter: "agent", IsAdmin: true}, "quota_refresh", json.RawMessage(`{"auth_index":"codex-reset"}`), "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "error" || result.Code != "rate_limited_backoff" || !strings.Contains(result.Detail, "retry after") {
		t.Fatalf("agent refusal = %+v", result)
	}
	if fixture.usageReads("codex-reset") != 0 {
		t.Fatal("parked Agent refresh reached the upstream")
	}
}

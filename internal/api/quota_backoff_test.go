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

	"github.com/oh-my-cpa/oh-my-cpa/internal/crypto"
	"github.com/oh-my-cpa/oh-my-cpa/internal/domain"
)

// TestManualRefreshHonoursAndRecordsRateLimitBackoff drives the console refresh
// path against an upstream that answers 429 with a Retry-After, and proves the
// second attempt is refused without reaching the upstream again.
func TestManualRefreshHonoursAndRecordsRateLimitBackoff(t *testing.T) {
	const managementKey = "management-secret"
	var apiCalls int64

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
			_, _ = writer.Write([]byte(`{"status_code":429,"header":{"Retry-After":["120"]},"body":{"message":"slow down"}}`))
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
}

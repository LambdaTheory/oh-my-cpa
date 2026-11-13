package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/cpa/management"
	"github.com/oh-my-cpa/oh-my-cpa/internal/crypto"
	"github.com/oh-my-cpa/oh-my-cpa/internal/domain"
	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
)

// TestPluginQuotaRateLimitParksTheCredential drives the console refresh path for
// a plugin-provided credential: the plugin route's 429 must become the shared
// rate-limit refusal, the attempt must be recorded as rate_limited with the
// plugin's retry time, and a second refresh inside that window must not POST to
// the plugin again. Once the window passes, the next refresh probes again.
func TestPluginQuotaRateLimitParksTheCredential(t *testing.T) {
	const managementKey = "management-secret"
	var pluginPosts int64
	cpaServer := newFakeCPA(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+managementKey {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch request.URL.Path {
		case "/v8/management/plugins/ollama-quota/quota":
			atomic.AddInt64(&pluginPosts, 1)
			writer.Header().Set("Content-Type", "application/json")
			writer.Header().Set("Retry-After", "120")
			writer.WriteHeader(http.StatusTooManyRequests)
			_, _ = writer.Write([]byte(`{"error":"rate_limited","message":"upstream rate limited the quota read"}`))
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer cpaServer.Close()

	handler := testHandler(t, "")
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
	ctx := context.Background()
	if err := handler.repo.UpsertInstance(ctx, domain.CPAInstance{
		ID: "default", Name: "Default", BaseURL: cpaServer.URL,
		UsageAddr:               strings.TrimPrefix(cpaServer.URL, "http://"),
		ManagementKeyCiphertext: ciphertext, ManagementKeyNonce: nonce,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	client, err := handler.capabilityClient(ctx)
	if err != nil {
		t.Fatal(err)
	}
	file := management.AuthFile{
		AuthIndex: "auth-plugin-rate-limited", Name: "ollama-rate-limited.json",
		Type: "ollama", Provider: "ollama", SupportsQuota: true,
		QuotaProvider: "ollama", QuotaPluginID: "ollama-quota",
	}

	_, err = handler.readCredentialQuota(ctx, quota.NewService(client), file, nil)
	var rateErr *quota.RateLimitError
	if !errors.As(err, &rateErr) || !rateErr.HasRetryAfter || rateErr.RetryAfter != 120*time.Second {
		t.Fatalf("first refresh = %v, want the plugin's 120s rate-limit refusal", err)
	}
	if got := atomic.LoadInt64(&pluginPosts); got != 1 {
		t.Fatalf("plugin POSTs = %d, want 1", got)
	}
	attempts, err := handler.repo.GetQuotaRefreshAttempts(ctx, []string{file.AuthIndex})
	if err != nil {
		t.Fatal(err)
	}
	attempt := attempts[file.AuthIndex]
	if attempt.Outcome != "rate_limited" || attempt.HTTPStatus != http.StatusTooManyRequests || attempt.RetryAtMS != now.Add(120*time.Second).UnixMilli() {
		t.Fatalf("attempt = %+v, want rate_limited at +120s", attempt)
	}

	// A refresh inside the park is refused without reaching the plugin again.
	if _, err = handler.readCredentialQuota(ctx, quota.NewService(client), file, nil); !errors.As(err, &rateErr) {
		t.Fatalf("second refresh = %v, want the backoff refusal", err)
	}
	if got := atomic.LoadInt64(&pluginPosts); got != 1 {
		t.Fatalf("a parked credential posted to the plugin again: %d POSTs", got)
	}

	// Once the Retry-After window passes, the credential is probed again.
	now = now.Add(121 * time.Second)
	if _, err = handler.readCredentialQuota(ctx, quota.NewService(client), file, nil); !errors.As(err, &rateErr) {
		t.Fatalf("refresh after the window = %v, want the plugin's fresh refusal", err)
	}
	if got := atomic.LoadInt64(&pluginPosts); got != 2 {
		t.Fatalf("plugin POSTs after the window = %d, want 2", got)
	}
}

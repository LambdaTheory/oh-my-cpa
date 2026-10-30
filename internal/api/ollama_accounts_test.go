package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/oh-my-cpa/oh-my-cpa/internal/config"
	"github.com/oh-my-cpa/oh-my-cpa/internal/crypto"
	"github.com/oh-my-cpa/oh-my-cpa/internal/ollama"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

const (
	ollamaTestCookie = "super-secret-ollama-cookie-value"
	ollamaTestAPIKey = "super-secret-ollama-api-key-value"
)

func startOllamaTestServer(t *testing.T) (*httptest.Server, *repository.Repository, *crypto.Cipher) {
	t.Helper()
	cipher, err := crypto.New("01234567890123456789012345678901")
	if err != nil {
		t.Fatal(err)
	}
	db, err := repository.Open(context.Background(), dashboardMemoryDSN("ollama"), repository.WithCipher(cipher))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := repository.New(db)
	handler := NewHandler(config.Config{BasePath: "/omc", Version: "test"}, repo, cipher, nil, nil)
	router := chi.NewRouter()
	// RegisterOllamaRoutes is the standalone registration the integration owner calls
	// inside the authenticated v1 group; here it is mounted raw so the handlers can be
	// exercised without the shared routing table.
	RegisterOllamaRoutes(router, handler)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return server, repo, cipher
}

func ollamaRequest(t *testing.T, method, url, body string) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = bytes.NewBufferString(body)
	}
	request, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, payload
}

// TestOllamaAccountResponsesNeverLeakCookie pins that create, list, detail and update
// responses carry only presence flags and never the cookie or API key text, and that an
// update omitting both credentials preserves them.
func TestOllamaAccountResponsesNeverLeakCookie(t *testing.T) {
	server, repo, cipher := startOllamaTestServer(t)

	status, payload := ollamaRequest(t, http.MethodPost, server.URL+"/ollama/accounts",
		`{"label":"work","cookie":"`+ollamaTestCookie+`","api_key":"`+ollamaTestAPIKey+`","enabled":true}`)
	if status != http.StatusCreated {
		t.Fatalf("create status = %d: %s", status, payload)
	}
	assertNoOllamaSecrets(t, payload)
	var created ollamaAccountDTO
	if err := json.Unmarshal(payload, &created); err != nil {
		t.Fatal(err)
	}
	if !created.HasCookie || !created.HasAPIKey || created.ID == "" {
		t.Fatalf("create dto = %+v", created)
	}

	status, payload = ollamaRequest(t, http.MethodGet, server.URL+"/ollama/accounts", "")
	if status != http.StatusOK {
		t.Fatalf("list status = %d", status)
	}
	assertNoOllamaSecrets(t, payload)
	if !strings.Contains(string(payload), `"has_cookie":true`) ||
		!strings.Contains(string(payload), `"has_api_key":true`) {
		t.Fatalf("list must report both presence flags: %s", payload)
	}

	status, payload = ollamaRequest(t, http.MethodGet, server.URL+"/ollama/accounts/"+created.ID, "")
	if status != http.StatusOK {
		t.Fatalf("detail status = %d", status)
	}
	assertNoOllamaSecrets(t, payload)

	// An update with no credentials keeps both stored ones.
	status, payload = ollamaRequest(t, http.MethodPut, server.URL+"/ollama/accounts/"+created.ID, `{"label":"renamed"}`)
	if status != http.StatusOK {
		t.Fatalf("update status = %d: %s", status, payload)
	}
	assertNoOllamaSecrets(t, payload)
	if !strings.Contains(string(payload), `"has_cookie":true`) ||
		!strings.Contains(string(payload), `"has_api_key":true`) {
		t.Fatalf("update must keep both presence flags: %s", payload)
	}
	secrets, err := repo.ListEnabledOllamaAccountSecrets(context.Background())
	if err != nil || len(secrets) != 1 {
		t.Fatalf("secrets = %+v %v", secrets, err)
	}
	decrypted, err := cipher.Decrypt(secrets[0].Ciphertext, secrets[0].Nonce)
	if err != nil || string(decrypted) != ollamaTestCookie {
		t.Fatalf("an omitted cookie must be preserved: %q %v", decrypted, err)
	}
	decryptedKey, err := cipher.Decrypt(secrets[0].APIKeyCiphertext, secrets[0].APIKeyNonce)
	if err != nil || string(decryptedKey) != ollamaTestAPIKey {
		t.Fatalf("an omitted API key must be preserved: %q %v", decryptedKey, err)
	}

	// Present but blank credentials are refused rather than clearing them.
	if status, _ := ollamaRequest(t, http.MethodPut, server.URL+"/ollama/accounts/"+created.ID, `{"cookie":""}`); status != http.StatusBadRequest {
		t.Fatalf("blank cookie update status = %d, want 400", status)
	}
	if status, _ := ollamaRequest(t, http.MethodPut, server.URL+"/ollama/accounts/"+created.ID, `{"api_key":""}`); status != http.StatusBadRequest {
		t.Fatalf("blank API key update status = %d, want 400", status)
	}

	if status, _ := ollamaRequest(t, http.MethodDelete, server.URL+"/ollama/accounts/"+created.ID, ""); status != http.StatusNoContent {
		t.Fatalf("delete status = %d", status)
	}
}

// TestOllamaBalanceJSONProjectsIntoQuotaDTO pins the key path end to end: a
// realistic balance document becomes the credits object the quota DTO sends. The
// endpoint's two USD figures must survive the projection, while data it does not
// publish stays absent instead of becoming zero.
func TestOllamaBalanceJSONProjectsIntoQuotaDTO(t *testing.T) {
	const body = `{"included":{"balance_usd":"12.50","allowance_usd":"20.00","period":{"from":"2026-10-01T00:00:00Z","until":"2026-11-01T00:00:00Z"}},"purchased":{"balance_usd":"3.00"}}`
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if got, want := request.Header.Get("Authorization"), "Bearer "+ollamaTestAPIKey; got != want {
			t.Errorf("Authorization = %q, want %q", got, want)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, body)
	}))
	t.Cleanup(upstream.Close)

	client := ollama.NewClient(time.Second, ollama.WithBalanceURL(upstream.URL))
	snapshot, err := client.FetchBalance(context.Background(), "acct-balance", ollamaTestAPIKey)
	if err != nil {
		t.Fatalf("fetch balance: %v", err)
	}
	if !snapshot.IsSuccess() {
		t.Fatalf("balance snapshot must be a success: %+v", snapshot)
	}
	encoded, err := json.Marshal(snapshot.ToQuota("Work"))
	if err != nil {
		t.Fatal(err)
	}

	dto := ollamaQuotaDTO{AccountID: "acct-balance", Quota: decodeOllamaQuota(string(encoded))}
	wire, err := json.Marshal(dto)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ollamaQuotaDTO
	if err := json.Unmarshal(wire, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Quota == nil {
		t.Fatal("the DTO lost the stored quota")
	}
	credits := decoded.Quota.OllamaCredits
	if credits == nil {
		t.Fatal("the DTO lost ollama_credits")
	}
	if credits.Source != "balance_api" {
		t.Fatalf("source = %q, want balance_api", credits.Source)
	}
	if credits.RemainingCents == nil || *credits.RemainingCents != 1250 ||
		credits.UsedCents == nil || *credits.UsedCents != 750 ||
		credits.TotalCents == nil || *credits.TotalCents != 2000 {
		t.Fatalf("balance amounts = %+v, want 1250/750/2000", credits)
	}
	if credits.UsedPercent == nil || *credits.UsedPercent != 37.5 {
		t.Fatalf("used percent = %v, want 37.5", credits.UsedPercent)
	}
	if credits.RefillText != "" || len(credits.Models) != 0 {
		t.Fatalf("balance endpoint must not invent refill or model facts: %+v", credits)
	}
	if decoded.Quota.Plan != nil || len(decoded.Quota.Windows) != 0 {
		t.Fatalf("balance path must not invent plan or windows: plan=%+v windows=%+v", decoded.Quota.Plan, decoded.Quota.Windows)
	}
}

func TestOllamaQuotaReadIsReadOnly(t *testing.T) {
	server, repo, _ := startOllamaTestServer(t)
	status, payload := ollamaRequest(t, http.MethodPost, server.URL+"/ollama/accounts",
		`{"label":"work","cookie":"`+ollamaTestCookie+`"}`)
	if status != http.StatusCreated {
		t.Fatalf("create status = %d: %s", status, payload)
	}
	var created ollamaAccountDTO
	if err := json.Unmarshal(payload, &created); err != nil {
		t.Fatal(err)
	}
	snapshot := `{"auth_index":"ollama:` + created.ID + `","provider":"ollama","windows":[],"unexpected_secret":"must-not-cross","ollama_credits":{"source":"settings_usage_credits","remaining_cents":29998,"used_cents":2,"total_cents":30000,"refill_text":"Monthly credits refill in 2 weeks","models":[{"model":"fake-model:1017","requests":15}],"unexpected_secret":"must-not-cross"}}`
	if _, _, err := repo.RecordOllamaResult(context.Background(), repository.OllamaCollectionResult{
		ID: created.ID, Status: "ok", ObservedAtMS: 5000, IsSuccess: true, SnapshotJSON: snapshot,
	}); err != nil {
		t.Fatal(err)
	}
	status, payload = ollamaRequest(t, http.MethodGet, server.URL+"/ollama/quota", "")
	if status != http.StatusOK {
		t.Fatalf("quota status = %d", status)
	}
	assertNoCookie(t, payload)
	if strings.Contains(string(payload), "must-not-cross") {
		t.Fatal("unlisted stored fields crossed DTO boundary")
	}
	var response ollamaQuotaResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatal(err)
	}
	credits := response.Accounts[0].Quota.OllamaCredits
	if credits == nil || *credits.RemainingCents != 29998 || *credits.UsedCents != 2 || *credits.TotalCents != 30000 || len(credits.Models) != 1 || credits.Models[0].Requests != 15 {
		t.Fatalf("wrong USD wire: %+v", credits)
	}
	if !strings.Contains(string(payload), `"observed_at_ms":5000`) || !strings.Contains(string(payload), `ollama:`+created.ID) {
		t.Fatalf("quota body = %s", payload)
	}
}

func assertNoCookie(t *testing.T, payload []byte) {
	t.Helper()
	assertNoOllamaSecrets(t, payload)
}

// assertNoOllamaSecrets covers every ordinary account response: neither plaintext
// credential, nor a ciphertext or nonce field, may cross the DTO boundary.
func assertNoOllamaSecrets(t *testing.T, payload []byte) {
	t.Helper()
	text := string(payload)
	for _, secret := range []string{
		ollamaTestCookie,
		ollamaTestAPIKey,
		"cookie_ciphertext",
		"cookie_nonce",
		"api_key_ciphertext",
		"api_key_nonce",
	} {
		if strings.Contains(text, secret) {
			t.Fatalf("secret material %q leaked into a response: %s", secret, text)
		}
	}
}

func ollamaWrite(t *testing.T, client *http.Client, base, method, path, body string) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = bytes.NewBufferString(body)
	}
	request, err := http.NewRequest(method, base+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Origin", base)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, payload
}

// TestOllamaRefreshTriggerDecision pins the truth table the update route uses: only
// an enabled account is scraped, and only a credential change or a resume warrants it.
func TestOllamaRefreshTriggerDecision(t *testing.T) {
	cases := []struct {
		name                      string
		previous, enabled         bool
		cookieChanged, keyChanged bool
		want                      bool
	}{
		{"resume", false, true, false, false, true},
		{"cookie change", true, true, true, false, true},
		{"api key change", true, true, false, true, true},
		{"resume and cookie change", false, true, true, false, true},
		{"label only", true, true, false, false, false},
		{"pause", true, false, false, false, false},
		{"paused cookie change", false, false, true, false, false},
		{"paused api key change", false, false, false, true, false},
		{"no change", true, true, false, false, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := shouldRefreshOllamaAccount(testCase.previous, testCase.enabled, testCase.cookieChanged, testCase.keyChanged); got != testCase.want {
				t.Fatalf("shouldRefreshOllamaAccount(%v, %v, %v, %v) = %v, want %v",
					testCase.previous, testCase.enabled, testCase.cookieChanged, testCase.keyChanged, got, testCase.want)
			}
		})
	}
}

// TestOllamaRefreshEndpointRunsTheScrape exercises the manual route through the real
// router, session and same-origin middleware, over a collector whose Fetch is
// injected: the scrape runs, the response carries the metadata and safe snapshot,
// and the cookie never appears.
func TestOllamaRefreshEndpointRunsTheScrape(t *testing.T) {
	var fetches atomic.Int32
	client, base, _ := startDashboardTestServer(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{}`))
	}, func(handler *Handler) {
		service := handler.BuildOllamaService()
		if service == nil {
			t.Fatal("the collector service must build over the test repository")
		}
		service.Fetch = func(_ context.Context, account ollama.Account, cookie string) (ollama.Snapshot, error) {
			fetches.Add(1)
			if cookie != ollamaTestCookie {
				t.Errorf("fetch cookie = %q", cookie)
			}
			used := 42.0
			return ollama.Snapshot{
				AccountID:  account.ID,
				ObservedAt: time.Now(),
				Windows:    []ollama.Window{{Name: "5h", Label: "Ollama 5-hour", UsedPercent: &used}},
			}, nil
		}
		// The application installs the lifecycle context before serving; a manual
		// refresh before that would be refused with 503.
		service.Start(context.Background())
		handler.SetOllamaService(service)
	})

	status, payload := ollamaWrite(t, client, base, http.MethodPost, "/omc/api/v1/ollama/accounts",
		`{"label":"work","cookie":"`+ollamaTestCookie+`"}`)
	if status != http.StatusCreated {
		t.Fatalf("create status = %d: %s", status, payload)
	}
	var created ollamaAccountDTO
	if err := json.Unmarshal(payload, &created); err != nil {
		t.Fatal(err)
	}

	status, payload = ollamaWrite(t, client, base, http.MethodPost, "/omc/api/v1/ollama/accounts/"+created.ID+"/refresh", "")
	if status != http.StatusOK {
		t.Fatalf("refresh status = %d: %s", status, payload)
	}
	assertNoCookie(t, payload)
	var refreshed ollamaRefreshResponse
	if err := json.Unmarshal(payload, &refreshed); err != nil {
		t.Fatal(err)
	}
	if !refreshed.Refreshed {
		t.Fatalf("refresh = %+v, want refreshed true", refreshed)
	}
	if refreshed.Account.ID != created.ID || refreshed.Account.Status != "ok" {
		t.Fatalf("refresh account = %+v", refreshed.Account)
	}
	if refreshed.Quota == nil || refreshed.Quota.AuthIndex != "ollama:"+created.ID {
		t.Fatalf("refresh quota = %+v", refreshed.Quota)
	}
	if fetches.Load() != 1 {
		t.Fatalf("fetches = %d, want 1", fetches.Load())
	}

	// A second immediate click inside the manual gap is suppressed rather than
	// scraping again.
	status, payload = ollamaWrite(t, client, base, http.MethodPost, "/omc/api/v1/ollama/accounts/"+created.ID+"/refresh", "")
	if status != http.StatusTooManyRequests {
		t.Fatalf("throttled refresh status = %d: %s", status, payload)
	}
	if err := json.Unmarshal(payload, &refreshed); err != nil {
		t.Fatal(err)
	}
	if refreshed.Refreshed || refreshed.RetryAtMS == nil {
		t.Fatalf("throttled refresh = %+v, want refreshed false with retry_at_ms", refreshed)
	}
	if fetches.Load() != 1 {
		t.Fatalf("a throttled refresh scraped again: fetches = %d", fetches.Load())
	}
}

// TestOllamaRefreshEndpointUnavailableWithoutService proves the collector-off (and
// demo) case: the route reports 503 and starts no fallback collector. The read-only
// routes are unaffected.
func TestOllamaRefreshEndpointUnavailableWithoutService(t *testing.T) {
	client, base, _ := startDashboardTestServer(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{}`))
	})

	status, payload := ollamaWrite(t, client, base, http.MethodPost, "/omc/api/v1/ollama/accounts",
		`{"label":"work","cookie":"`+ollamaTestCookie+`"}`)
	if status != http.StatusCreated {
		t.Fatalf("create status = %d: %s", status, payload)
	}
	var created ollamaAccountDTO
	if err := json.Unmarshal(payload, &created); err != nil {
		t.Fatal(err)
	}
	status, payload = ollamaWrite(t, client, base, http.MethodPost, "/omc/api/v1/ollama/accounts/"+created.ID+"/refresh", "")
	if status != http.StatusServiceUnavailable {
		t.Fatalf("refresh without a service = %d: %s", status, payload)
	}
	// The read still works.
	response, body := getJSON(t, client, base+"/omc/api/v1/ollama/accounts")
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), created.ID) {
		t.Fatalf("account list = %d %s", response.StatusCode, body)
	}
}

// TestOllamaCreateAndCookieUpdateTriggerScrapes proves the integration wiring: a
// newly created enabled account is scraped without waiting for the interval, and a
// cookie update scrapes again with the new cookie once the account is idle, through
// the shared service the handler and the trigger worker both hold. A cookie update
// that collides with an in-flight scrape is the accepted dropped case (the next
// cadence picks it up), so this test waits for the first scrape to finish.
func TestOllamaCreateAndCookieUpdateTriggerScrapes(t *testing.T) {
	scrapes := make(chan string, 16)
	var service *ollama.Collector
	var handler *Handler
	client, base, _ := startDashboardTestServer(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{}`))
	}, func(h *Handler) {
		handler = h
		service = h.BuildOllamaService()
		service.Fetch = func(_ context.Context, account ollama.Account, cookie string) (ollama.Snapshot, error) {
			scrapes <- cookie
			used := 20.0
			return ollama.Snapshot{
				AccountID:  account.ID,
				ObservedAt: time.Now(),
				Windows:    []ollama.Window{{Name: "5h", Label: "Ollama 5-hour", UsedPercent: &used}},
			}, nil
		}
		// The periodic wait never fires; only the triggers under test drive scrapes.
		service.After = func(time.Duration) <-chan time.Time { return make(chan time.Time) }
		h.SetOllamaService(service)
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan struct{})
	go func() { _ = service.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("the collector did not stop on shutdown")
		}
	})

	status, payload := ollamaWrite(t, client, base, http.MethodPost, "/omc/api/v1/ollama/accounts",
		`{"label":"work","cookie":"first-cookie-value"}`)
	if status != http.StatusCreated {
		t.Fatalf("create status = %d: %s", status, payload)
	}
	var created ollamaAccountDTO
	if err := json.Unmarshal(payload, &created); err != nil {
		t.Fatal(err)
	}
	waitForScrapedCookie(t, scrapes, "first-cookie-value")
	waitUntilNotRefreshing(t, handler, created.ID)

	status, payload = ollamaWrite(t, client, base, http.MethodPut, "/omc/api/v1/ollama/accounts/"+created.ID,
		`{"cookie":"second-cookie-value"}`)
	if status != http.StatusOK {
		t.Fatalf("cookie update status = %d: %s", status, payload)
	}
	waitForScrapedCookie(t, scrapes, "second-cookie-value")
}

// TestOllamaCookieUpdateDuringInflightScrapeIsCollected pins the fixed trigger: a
// cookie replaced while an automatic scrape is in flight is not dropped until the
// cadence. The old round finishes, its result is refused because the cookie changed,
// and the worker takes the new cookie in one more round - without the clock advancing,
// so the cadence never contributed. The old round is a 429 whose backoff would suppress
// the follow-up if it had been written, so the follow-up is also the proof the stale
// result was refused.
func TestOllamaCookieUpdateDuringInflightScrapeIsCollected(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	firstStarted := make(chan struct{}, 1)
	release := make(chan struct{})
	scrapes := make(chan string, 8)
	var calls atomic.Int32

	client, base, _ := startDashboardTestServer(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{}`))
	}, func(handler *Handler) {
		service := handler.BuildOllamaService()
		service.Fetch = func(_ context.Context, account ollama.Account, cookie string) (ollama.Snapshot, error) {
			callNumber := calls.Add(1)
			scrapes <- cookie
			if callNumber == 1 {
				firstStarted <- struct{}{}
				<-release
				return ollama.Snapshot{
					AccountID: account.ID, ObservedAt: time.Now(),
					Failure: ollama.FailureTransport, Err: "rate limited",
					RetryAfter: time.Hour, HasRetryAfter: true,
				}, nil
			}
			used := 99.0
			return ollama.Snapshot{
				AccountID:  account.ID,
				ObservedAt: time.Now(),
				Windows:    []ollama.Window{{Name: "5h", Label: "Ollama 5-hour", UsedPercent: &used}},
			}, nil
		}
		// The cadence never fires: the follow-up must come from the trigger, not a pass.
		service.After = func(time.Duration) <-chan time.Time { return make(chan time.Time) }
		handler.SetOllamaService(service)
		go func() { _ = service.Run(ctx) }()
	})

	status, payload := ollamaWrite(t, client, base, http.MethodPost, "/omc/api/v1/ollama/accounts",
		`{"label":"work","cookie":"first-cookie-value"}`)
	if status != http.StatusCreated {
		t.Fatalf("create status = %d: %s", status, payload)
	}
	var created ollamaAccountDTO
	if err := json.Unmarshal(payload, &created); err != nil {
		t.Fatal(err)
	}

	select {
	case <-firstStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("the first scrape never started")
	}

	// Replace the cookie while the first scrape is in flight, then let it finish.
	if status, body := ollamaWrite(t, client, base, http.MethodPut, "/omc/api/v1/ollama/accounts/"+created.ID,
		`{"cookie":"second-cookie-value"}`); status != http.StatusOK {
		t.Fatalf("cookie update status = %d: %s", status, body)
	}
	close(release)

	waitForScrapedCookie(t, scrapes, "second-cookie-value")
	if calls.Load() < 2 {
		t.Fatalf("the new cookie was not collected: fetches = %d", calls.Load())
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		_, body := getJSON(t, client, base+"/omc/api/v1/ollama/accounts/"+created.ID)
		var account ollamaAccountDTO
		if err := json.Unmarshal(body, &account); err == nil && account.Status == ollama.StatusOK {
			if account.NextAttemptAtMS != nil {
				t.Fatalf("the stale 429 backoff survived: %s", body)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the account never turned ok: %s", body)
		}
		runtime.Gosched()
	}
	if _, quotaBody := getJSON(t, client, base+"/omc/api/v1/ollama/quota"); !strings.Contains(string(quotaBody), `"used_percent":99`) {
		t.Fatalf("the follow-up reading was not persisted: %s", quotaBody)
	}
}

// TestOllamaRefreshEndpointBeforeStartIsRefused proves the startup window is safe:
// a collector built but not yet started refuses the manual refresh with 503 instead
// of running a scrape on the request context.
func TestOllamaRefreshEndpointBeforeStartIsRefused(t *testing.T) {
	var fetches atomic.Int32
	client, base, _ := startDashboardTestServer(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{}`))
	}, func(handler *Handler) {
		service := handler.BuildOllamaService()
		service.Fetch = func(_ context.Context, account ollama.Account, _ string) (ollama.Snapshot, error) {
			fetches.Add(1)
			used := 10.0
			return ollama.Snapshot{AccountID: account.ID, ObservedAt: time.Now(), Windows: []ollama.Window{{Name: "5h", UsedPercent: &used}}}, nil
		}
		// Deliberately not started: this is the pre-listener window.
		handler.SetOllamaService(service)
	})

	status, payload := ollamaWrite(t, client, base, http.MethodPost, "/omc/api/v1/ollama/accounts",
		`{"label":"work","cookie":"`+ollamaTestCookie+`"}`)
	if status != http.StatusCreated {
		t.Fatalf("create status = %d: %s", status, payload)
	}
	var created ollamaAccountDTO
	if err := json.Unmarshal(payload, &created); err != nil {
		t.Fatal(err)
	}
	status, payload = ollamaWrite(t, client, base, http.MethodPost, "/omc/api/v1/ollama/accounts/"+created.ID+"/refresh", "")
	if status != http.StatusServiceUnavailable {
		t.Fatalf("refresh before Start = %d: %s", status, payload)
	}
	if fetches.Load() != 0 {
		t.Fatalf("a pre-start refresh scraped: fetches = %d", fetches.Load())
	}
}

// TestOllamaPausedAutomaticResultIsDroppedAndResumeScrapes pins the explicit policy:
// an automatic scrape that finishes after the account was paused does not write its
// 429 (status, error, backoff or attempt count), so a later resume is not suppressed
// by a stale backoff and scrapes normally. What happens when a disable and a resume
// both land under one in-flight scrape is the accepted race documented in
// docs/ollama-monitoring.md and is deliberately not asserted here.
func TestOllamaPausedAutomaticResultIsDroppedAndResumeScrapes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	autoStarted := make(chan struct{}, 1)
	autoRelease := make(chan struct{})
	var calls atomic.Int32
	var handler *Handler

	client, base, _ := startDashboardTestServer(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{}`))
	}, func(h *Handler) {
		handler = h
		service := h.BuildOllamaService()
		service.Fetch = func(_ context.Context, account ollama.Account, _ string) (ollama.Snapshot, error) {
			if calls.Add(1) == 1 {
				autoStarted <- struct{}{}
				<-autoRelease
				return ollama.Snapshot{
					AccountID: account.ID, ObservedAt: time.Now(),
					Failure: ollama.FailureTransport, Err: "rate limited",
					RetryAfter: time.Hour, HasRetryAfter: true,
				}, nil
			}
			used := 33.0
			return ollama.Snapshot{
				AccountID:  account.ID,
				ObservedAt: time.Now(),
				Windows:    []ollama.Window{{Name: "5h", Label: "Ollama 5-hour", UsedPercent: &used}},
			}, nil
		}
		service.After = func(time.Duration) <-chan time.Time { return make(chan time.Time) }
		h.SetOllamaService(service)
		go func() { _ = service.Run(ctx) }()
	})

	status, payload := ollamaWrite(t, client, base, http.MethodPost, "/omc/api/v1/ollama/accounts",
		`{"label":"work","cookie":"`+ollamaTestCookie+`"}`)
	if status != http.StatusCreated {
		t.Fatalf("create status = %d: %s", status, payload)
	}
	var created ollamaAccountDTO
	if err := json.Unmarshal(payload, &created); err != nil {
		t.Fatal(err)
	}

	select {
	case <-autoStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("the automatic scrape never started")
	}

	// Pause the account while the automatic scrape is in flight, then let it finish.
	if status, body := ollamaWrite(t, client, base, http.MethodPut, "/omc/api/v1/ollama/accounts/"+created.ID, `{"enabled":false}`); status != http.StatusOK {
		t.Fatalf("disable status = %d: %s", status, body)
	}
	close(autoRelease)
	waitUntilNotRefreshing(t, handler, created.ID)

	response, body := getJSON(t, client, base+"/omc/api/v1/ollama/accounts/"+created.ID)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("detail status = %d: %s", response.StatusCode, body)
	}
	var paused ollamaAccountDTO
	if err := json.Unmarshal(body, &paused); err != nil {
		t.Fatal(err)
	}
	if paused.Status != "" || paused.NewestError != "" || paused.NextAttemptAtMS != nil || paused.AttemptCount != 0 {
		t.Fatalf("a paused automatic result was written: %s", body)
	}

	// Resume: the trigger scrapes normally because no stale backoff was left behind.
	if status, body := ollamaWrite(t, client, base, http.MethodPut, "/omc/api/v1/ollama/accounts/"+created.ID, `{"enabled":true}`); status != http.StatusOK {
		t.Fatalf("resume status = %d: %s", status, body)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		_, body := getJSON(t, client, base+"/omc/api/v1/ollama/accounts/"+created.ID)
		var account ollamaAccountDTO
		if err := json.Unmarshal(body, &account); err == nil && account.Status == ollama.StatusOK {
			if account.NextAttemptAtMS != nil {
				t.Fatalf("a stale backoff survived the resume: %s", body)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the resumed account never produced a reading: %s", body)
		}
		runtime.Gosched()
	}
	if calls.Load() < 2 {
		t.Fatalf("the resume trigger did not scrape: fetches = %d", calls.Load())
	}
	if _, quotaBody := getJSON(t, client, base+"/omc/api/v1/ollama/quota"); !strings.Contains(string(quotaBody), created.ID) {
		t.Fatalf("the resumed reading was not persisted: %s", quotaBody)
	}
}

func waitUntilNotRefreshing(t *testing.T, handler *Handler, id string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for handler.ollama.Refreshing(id) {
		if time.Now().After(deadline) {
			t.Fatal("the in-flight scrape did not finish")
		}
		runtime.Gosched()
	}
}

func waitForScrapedCookie(t *testing.T, scrapes <-chan string, want string) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case cookie := <-scrapes:
			if cookie == want {
				return
			}
		case <-deadline:
			t.Fatalf("no scrape carried cookie %q", want)
		}
	}
}

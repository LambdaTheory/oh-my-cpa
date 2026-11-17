package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/crypto"
	"github.com/oh-my-cpa/oh-my-cpa/internal/domain"
)

// compatibilityQuotaFixture is a gateway that lists one Command Code auth file
// and one opencode compatibility key, and counts the upstream reads each
// refresh makes. The two providers reach their upstreams through different
// routes: Command Code reads two billing documents, OpenCode reads one usage
// document.
type compatibilityQuotaFixture struct {
	mu          sync.Mutex
	reads       map[string]int
	urlReads    map[string]int
	discoveryUp bool
}

func (f *compatibilityQuotaFixture) readCount(authIndex string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads[authIndex]
}

func (f *compatibilityQuotaFixture) urlReadCount(suffix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.urlReads[suffix]
}

func newCompatibilityQuotaFixture() *compatibilityQuotaFixture {
	return &compatibilityQuotaFixture{reads: map[string]int{}, urlReads: map[string]int{}}
}

func newCompatibilityQuotaHandler(t *testing.T, fixture *compatibilityQuotaFixture) *Handler {
	t.Helper()
	const managementKey = "management-secret"
	cpaServer := newFakeCPA(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+managementKey {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/v8/management/credentials":
			_, _ = writer.Write([]byte(`{"files":[
				{"id":"f-cmc","name":"commandcode-key-1.json","auth_index":"cmc-1","type":"commandcode","provider":"commandcode"}
			]}`))
		case "/v0/management/openai-compatibility":
			fixture.mu.Lock()
			hasDiscovery := fixture.discoveryUp
			fixture.mu.Unlock()
			if !hasDiscovery {
				writer.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = writer.Write([]byte(`{"openai-compatibility":[{
				"name":"opencode","base-url":"https://opencode.ai/zen/go/v1",
				"api-key-entries":[{"api-key":"sk-opencode-fixture","auth-index":"opc-1"}],
				"models":[{"name":"opencode-model"}]
			}]}`))
		case "/v8/management/requests/api-call":
			var payload struct {
				AuthIndex string `json:"auth_index"`
				URL       string `json:"url"`
			}
			_ = json.NewDecoder(request.Body).Decode(&payload)
			suffix := ""
			body := `{"status_code": 404, "body": {}}`
			switch {
			case strings.HasSuffix(payload.URL, "/alpha/billing/credits"):
				suffix = "credits"
				body = `{"status_code":200,"body":{
					"credits":{"monthlyCredits":50,"purchasedCredits":0},
					"windowLimits":{"limited":true,
						"fiveHour":{"used":10,"cap":100,"resetAt":1893456000000},
						"weekly":{"used":20,"cap":200,"resetAt":1896134400000}}}}`
			case strings.HasSuffix(payload.URL, "/alpha/billing/subscriptions"):
				suffix = "subscriptions"
				body = `{"status_code":200,"body":{"success":true,"data":{
					"planId":"individual-goat","status":"active","currentPeriodEnd":"2030-01-01T00:00:00Z"}}}`
			case strings.HasSuffix(payload.URL, "/usage"):
				suffix = "usage"
				body = `{"status_code":200,"body":{"usage":{
					"rolling":{"percent":5,"resetsAt":"2030-01-01T00:00:00Z"},
					"weekly":{"percent":42,"resetsAt":"2030-01-08T00:00:00Z"},
					"monthly":{"percent":21,"resetsAt":"2030-02-01T00:00:00Z"}}}}`
			default:
				_, _ = writer.Write([]byte(body))
				return
			}
			fixture.mu.Lock()
			fixture.reads[payload.AuthIndex]++
			fixture.urlReads[suffix]++
			fixture.mu.Unlock()
			_, _ = writer.Write([]byte(body))
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(cpaServer.Close)

	handler := testHandler(t, "")
	cipher, err := crypto.New("01234567890123456789012345678901")
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, nonce, err := cipher.Encrypt([]byte(managementKey))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := handler.repo.UpsertInstance(context.Background(), domain.CPAInstance{
		ID: "default", Name: "Default", BaseURL: cpaServer.URL,
		UsageAddr:               strings.TrimPrefix(cpaServer.URL, "http://"),
		ManagementKeyCiphertext: ciphertext, ManagementKeyNonce: nonce,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestQuotaAutoRefreshReadsTheOpenCodeEntryOnce(t *testing.T) {
	ctx := context.Background()
	fixture := newCompatibilityQuotaFixture()
	fixture.discoveryUp = true
	handler := newCompatibilityQuotaHandler(t, fixture)
	nowMS := time.Now().UnixMilli()
	saveAutoRefreshSnapshot(t, handler, "opc-1", "opencode", nowMS-5*time.Hour.Milliseconds(), nowMS-2*time.Minute.Milliseconds())
	setAutoRefreshPolicy(t, handler, `{"interval_minutes":0,"refresh_on_reset":true}`)

	round, err := handler.runQuotaAutoRefreshRound(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if round.Due != 1 || round.Refreshed != 1 || round.ByReset != 1 || round.Failed != 0 {
		t.Fatalf("round = %+v, want exactly the reset opencode key read", round)
	}
	if got := fixture.readCount("opc-1"); got != 1 {
		t.Fatalf("opencode reads = %d, want 1", got)
	}
	if got := fixture.urlReadCount("usage"); got != 1 {
		t.Fatalf("usage reads = %d, want 1", got)
	}
	snapshots, err := handler.repo.GetLatestQuotaSnapshots(ctx, []string{"opc-1"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := snapshots["opc-1"]
	if snapshot.Provider != "opencode" || snapshot.ObservedAtMS < nowMS || snapshot.Status == "" {
		t.Fatalf("stored reading = %+v, want a fresh opencode snapshot", snapshot)
	}
	var windows []map[string]any
	if err := json.Unmarshal([]byte(snapshot.WindowsJSON), &windows); err != nil || len(windows) != 3 {
		t.Fatalf("stored windows = %s, err=%v, want rolling/weekly/monthly", snapshot.WindowsJSON, err)
	}

	// The reading's resets are years away, so the same tick's follow-up must not
	// read it again.
	if round, err := handler.runQuotaAutoRefreshRound(ctx); err != nil || round.Due != 0 {
		t.Fatalf("second round = %+v err=%v, want nothing due", round, err)
	}
	if got := fixture.readCount("opc-1"); got != 1 {
		t.Fatalf("opencode reads after second round = %d, want 1", got)
	}
}

func TestQuotaManualRefreshAndOverviewIncludeCompatibilityCredentials(t *testing.T) {
	fixture := newCompatibilityQuotaFixture()
	fixture.discoveryUp = true
	handler := newCompatibilityQuotaHandler(t, fixture)
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

	refresh := func(authIndex string) (int, map[string]any) {
		t.Helper()
		response, err := client.Post(appServer.URL+"/api/v1/management/quota/refresh", "application/json",
			bytes.NewBufferString(fmt.Sprintf(`{"auth_index":%q}`, authIndex)))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var body map[string]any
		_ = json.NewDecoder(response.Body).Decode(&body)
		return response.StatusCode, body
	}
	if status, body := refresh("opc-1"); status != http.StatusOK {
		t.Fatalf("opencode refresh status = %d body=%v", status, body)
	}
	quota := refreshQuotaFromBody(t, refresh, "cmc-1")
	if quota.Provider != "commandcode" || len(quota.Windows) != 3 {
		t.Fatalf("commandcode reading = %+v", quota)
	}
	if fixture.urlReadCount("credits") != 1 || fixture.urlReadCount("subscriptions") != 1 {
		t.Fatalf("commandcode reads = credits %d, subscriptions %d, want 1 each",
			fixture.urlReadCount("credits"), fixture.urlReadCount("subscriptions"))
	}

	response, err := client.Get(appServer.URL + "/api/v1/management/quota")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var overview struct {
		Quotas []struct {
			AuthIndex string `json:"auth_index"`
			Provider  string `json:"provider"`
			Windows   []struct {
				ID   string `json:"id"`
				Kind string `json:"kind"`
			} `json:"windows"`
		} `json:"quotas"`
		Total int `json:"total"`
	}
	if err := json.NewDecoder(response.Body).Decode(&overview); err != nil {
		t.Fatal(err)
	}
	providers := map[string]string{}
	windows := map[string]int{}
	for _, item := range overview.Quotas {
		providers[item.AuthIndex] = item.Provider
		windows[item.AuthIndex] = len(item.Windows)
	}
	if providers["cmc-1"] != "commandcode" || windows["cmc-1"] != 3 {
		t.Fatalf("overview commandcode row = provider %q windows %d", providers["cmc-1"], windows["cmc-1"])
	}
	if providers["opc-1"] != "opencode" || windows["opc-1"] != 3 {
		t.Fatalf("overview opencode row = provider %q windows %d", providers["opc-1"], windows["opc-1"])
	}

	// The console detail page resolves the same auth index without it appearing
	// among CPA's auth files.
	detail, err := client.Get(appServer.URL + "/api/v1/management/quota/opc-1")
	if err != nil {
		t.Fatal(err)
	}
	defer detail.Body.Close()
	if detail.StatusCode != http.StatusOK {
		t.Fatalf("opencode detail status = %d", detail.StatusCode)
	}
}

type refreshedQuota struct {
	Provider string                       `json:"provider"`
	Windows  []map[string]json.RawMessage `json:"windows"`
}

// refreshQuotaFromBody performs one console refresh and returns the normalized
// reading, so a caller can assert both the transport and the projection.
func refreshQuotaFromBody(t *testing.T, refresh func(string) (int, map[string]any), authIndex string) refreshedQuota {
	t.Helper()
	status, body := refresh(authIndex)
	if status != http.StatusOK {
		t.Fatalf("%s refresh status = %d body=%v", authIndex, status, body)
	}
	raw, ok := body["quota"]
	if !ok {
		t.Fatalf("%s refresh body has no quota: %v", authIndex, body)
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	var quota refreshedQuota
	if err := json.Unmarshal(encoded, &quota); err != nil {
		t.Fatal(err)
	}
	return quota
}

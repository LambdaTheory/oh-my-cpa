package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/domain"
	"github.com/oh-my-cpa/oh-my-cpa/internal/feishu"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

// seedAuthFile stores one discovered auth file the way the discovery sync would,
// carrying the cached disable/unavailable flags and the sync instant.
func seedAuthFile(t *testing.T, repo *repository.Repository, authIndex, name, driver string, disabled, unavailable bool, syncedAt time.Time) {
	t.Helper()
	_, err := repo.UpsertDiscoveredResources(context.Background(), "default", []domain.DiscoveredResource{{
		ResourceKey:     "auth-index:auth-file:" + authIndex,
		CPAResourceType: "auth-file",
		CPAAuthIndex:    authIndex,
		CPAResourceName: name,
		CPADriver:       driver,
		Status:          domain.ResourceStatusUnclaimed,
		Details: domain.ResourceDetails{
			AuthType:    "oauth",
			Disabled:    disabled,
			Unavailable: unavailable,
		},
	}}, syncedAt, false)
	if err != nil {
		t.Fatal(err)
	}
}

// TestFeishuReportSourceReadsStoredData proves the bot's report is built from OMC's
// stored readings and the discovered credential metadata, with the real stored
// observation instants, and that a query reaches CPA not at all.
func TestFeishuReportSourceReadsStoredData(t *testing.T) {
	reference := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	var managementCalls atomic.Int64
	var handler *Handler
	_, _, repo := startDashboardTestServer(t, func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/v8/management") {
			// A stored-only source must never arrive here. Answer loudly if it does,
			// and count the call so the test fails on the count.
			managementCalls.Add(1)
			writer.WriteHeader(http.StatusInternalServerError)
			_, _ = writer.Write([]byte(`{"error":"a stored-only report must not contact CPA"}`))
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{}`))
	}, func(h *Handler) {
		handler = h
		h.nowFn = func() time.Time { return reference }
	})

	syncedAt := reference.Add(-6 * time.Hour)
	seedAuthFile(t, repo, "idx-codex", "codex.json", "codex", false, false, syncedAt)
	seedAuthFile(t, repo, "idx-claude", "claude.json", "claude", false, false, syncedAt)
	seedAuthFile(t, repo, "idx-unknown", "unknown.json", "mystery", false, false, syncedAt)

	// A healthy snapshot with a numeric weekly window and one with no numeric value.
	if err := repo.SaveQuotaSnapshot(context.Background(), repository.QuotaSnapshotRecord{
		AuthIndex: "idx-codex", Provider: "codex", Status: "healthy", ObservedAtMS: reference.Add(-2 * time.Minute).UnixMilli(),
		WindowsJSON: `[{"id":"weekly","label":"Weekly","scope":"standard","used_percent":42,"remaining_percent":58}]`,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveQuotaSnapshot(context.Background(), repository.QuotaSnapshotRecord{
		AuthIndex: "idx-claude", Provider: "claude", Status: "error", ObservedAtMS: reference.Add(-30 * time.Minute).UnixMilli(),
		WindowsJSON: `[{"id":"seven_day","label":"Weekly","scope":"standard"}]`,
	}); err != nil {
		t.Fatal(err)
	}
	// One credential is disabled by the operator. It must read as disabled, not as
	// an active, failing credential.
	seedAuthFile(t, repo, "idx-off", "off.json", "codex", true, false, syncedAt)
	if err := repo.SaveQuotaSnapshot(context.Background(), repository.QuotaSnapshotRecord{
		AuthIndex: "idx-off", Provider: "codex", Status: "needs_reauth", ObservedAtMS: reference.Add(-3 * time.Minute).UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}

	report, has := handler.BuildFeishuReportSource().ReadReport(context.Background())
	if !has {
		t.Fatal("a report with known channels must be available")
	}
	if calls := managementCalls.Load(); calls != 0 {
		t.Fatalf("a stored-only report made %d management requests, want 0", calls)
	}
	codex := reportProvider(t, report, "codex")
	if len(codex.Accounts) != 2 {
		t.Fatalf("codex accounts = %+v", codex.Accounts)
	}
	var healthy, off *feishu.AccountReport
	for i := range codex.Accounts {
		account := codex.Accounts[i]
		switch account.Key {
		case "idx-codex":
			healthy = &account
		case "idx-off":
			off = &account
		}
	}
	if healthy == nil || off == nil {
		t.Fatalf("codex accounts = %+v", codex.Accounts)
	}
	if healthy.State != "healthy" || !healthy.OK {
		t.Fatalf("codex account = %+v", healthy)
	}
	if len(healthy.Windows) != 1 || healthy.Windows[0].Scope != "account" || healthy.Windows[0].UsedPercent == nil || *healthy.Windows[0].UsedPercent != 42 {
		t.Fatalf("codex windows = %+v", healthy.Windows)
	}
	wantObserved := reference.Add(-2 * time.Minute).UTC()
	if !healthy.ObservedAt.Equal(wantObserved) {
		t.Fatalf("observed = %s, want the stored %s (never now)", healthy.ObservedAt, wantObserved)
	}
	// The disable flag and its sync time come from the stored metadata, never now.
	if !healthy.MetadataObservedAt.Equal(syncedAt) {
		t.Fatalf("metadata observed = %s, want the stored sync %s", healthy.MetadataObservedAt, syncedAt)
	}
	if !report.GeneratedAt.Equal(wantObserved) {
		t.Fatalf("generated = %s, want %s", report.GeneratedAt, wantObserved)
	}
	if !off.Disabled {
		t.Fatalf("a credential the operator disabled must read as disabled: %+v", off)
	}

	claude := reportProvider(t, report, "claude")
	if claude.Accounts[0].OK || claude.Accounts[0].State != "stale" {
		t.Fatalf("a window with no percentage must not be a valid reading: %+v", claude.Accounts[0])
	}
	unknown := reportProvider(t, report, "mystery")
	if len(unknown.Accounts) != 1 || unknown.Accounts[0].State != "unknown" || unknown.Accounts[0].OK {
		t.Fatalf("an unread credential is unknown, never healthy: %+v", unknown.Accounts)
	}
	if !unknown.Accounts[0].MetadataObservedAt.Equal(syncedAt) {
		t.Fatalf("an unread credential still carries its metadata sync time: %+v", unknown.Accounts[0])
	}
}

// TestFeishuReportSourceDisabledCredentialIsNotActiveEvidence proves the cached
// disable flag reaches the policy so a disabled credential is never the evidence
// for a credential failure: the account reads disabled and its state is the cached
// re-authentication problem, not an active failure.
func TestFeishuReportSourceDisabledCredentialIsNotActiveEvidence(t *testing.T) {
	reference := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	var managementCalls atomic.Int64
	var handler *Handler
	_, _, repo := startDashboardTestServer(t, func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/v8/management") {
			managementCalls.Add(1)
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = writer.Write([]byte(`{}`))
	}, func(h *Handler) {
		handler = h
		h.nowFn = func() time.Time { return reference }
	})

	seedAuthFile(t, repo, "idx-off", "off.json", "codex", true, false, reference.Add(-time.Hour))
	if err := repo.SaveQuotaSnapshot(context.Background(), repository.QuotaSnapshotRecord{
		AuthIndex: "idx-off", Provider: "codex", Status: "needs_reauth", ObservedAtMS: reference.Add(-time.Minute).UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}

	report, has := handler.BuildFeishuReportSource().ReadReport(context.Background())
	if !has {
		t.Fatal("a known channel must be available")
	}
	if calls := managementCalls.Load(); calls != 0 {
		t.Fatalf("a stored-only report made %d management requests, want 0", calls)
	}
	codex := reportProvider(t, report, "codex")
	if len(codex.Accounts) != 1 {
		t.Fatalf("codex accounts = %+v", codex.Accounts)
	}
	account := codex.Accounts[0]
	if !account.Disabled {
		t.Fatalf("the disabled credential must read as disabled: %+v", account)
	}
	if account.State != "invalid" {
		t.Fatalf("the cached re-auth problem must still be visible: %+v", account)
	}
}

// TestFeishuReportSourceNamesChannelsWhenMetadataIsMissing proves the documented
// limit of a fully stored source: with no stored credential metadata the channel is
// still named from the persisted observation timeline with a collector-level error,
// never by reaching for CPA inline.
func TestFeishuReportSourceNamesChannelsWhenMetadataIsMissing(t *testing.T) {
	var managementCalls atomic.Int64
	var handler *Handler
	_, _, repo := startDashboardTestServer(t, func(writer http.ResponseWriter, request *http.Request) {
		managementCalls.Add(1)
		writer.WriteHeader(http.StatusInternalServerError)
		_, _ = writer.Write([]byte(`{"error":"boom"}`))
	}, func(h *Handler) { handler = h })

	if _, err := repo.SQL().ExecContext(context.Background(), `
		INSERT INTO quota_observations (
			auth_index, provider, window_kind, scope, model, window_minutes, used_percent,
			reset_at_ms, status, observed_at_ms, event_key, created_at_ms, source
		) VALUES ('idx-codex','codex','weekly','standard','',10080,10,NULL,'',1,'k',1,'active')`); err != nil {
		t.Fatal(err)
	}

	report, has := handler.BuildFeishuReportSource().ReadReport(context.Background())
	if !has {
		t.Fatal("a known channel must still be reported when no metadata is stored")
	}
	if calls := managementCalls.Load(); calls != 0 {
		t.Fatalf("a stored-only report made %d management requests, want 0", calls)
	}
	codex := reportProvider(t, report, "codex")
	if codex.Error == "" || len(codex.Accounts) != 0 {
		t.Fatalf("collector-level failure = %+v", codex)
	}
}

// TestFeishuReportSourceIncludesOllamaReadings proves the Ollama module's persisted
// safe reading reaches the bot without any management request, and that a failed
// scrape is stale while an expired cookie is invalid.
func TestFeishuReportSourceIncludesOllamaReadings(t *testing.T) {
	reference := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	var managementCalls atomic.Int64
	var handler *Handler
	_, _, repo := startDashboardTestServer(t, func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/v8/management") {
			managementCalls.Add(1)
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = writer.Write([]byte(`{}`))
	}, func(h *Handler) {
		handler = h
		h.nowFn = func() time.Time { return reference }
	})

	account, err := repo.CreateOllamaAccount(context.Background(), repository.OllamaAccountCreate{
		Label: "work", Enabled: true, Ciphertext: []byte("cipher"), Nonce: []byte("nonce"), NowMS: reference.UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	observed := reference.Add(-5 * time.Minute).UnixMilli()
	snapshot := `{"auth_index":"ollama:` + account.ID + `","provider":"ollama","status":"healthy","observed_at_ms":` +
		itoa(observed) + `,"windows":[{"id":"5h","scope":"standard","used_percent":30}]}`
	if _, _, err := repo.RecordOllamaResult(context.Background(), repository.OllamaCollectionResult{
		ID: account.ID, Status: "ok", ObservedAtMS: observed, IsSuccess: true, SnapshotJSON: snapshot,
	}); err != nil {
		t.Fatal(err)
	}
	failed, err := repo.CreateOllamaAccount(context.Background(), repository.OllamaAccountCreate{
		Label: "broken", Enabled: true, Ciphertext: []byte("cipher"), Nonce: []byte("nonce"), NowMS: reference.UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.RecordOllamaResult(context.Background(), repository.OllamaCollectionResult{
		ID: failed.ID, Status: "auth_invalid", ObservedAtMS: reference.UnixMilli(), Error: "cookie invalid",
	}); err != nil {
		t.Fatal(err)
	}

	report, _ := handler.BuildFeishuReportSource().ReadReport(context.Background())
	if calls := managementCalls.Load(); calls != 0 {
		t.Fatalf("a stored-only report made %d management requests, want 0", calls)
	}
	ollama := reportProvider(t, report, "ollama")
	if len(ollama.Accounts) != 2 {
		t.Fatalf("ollama accounts = %+v", ollama.Accounts)
	}
	var healthy, invalid bool
	for _, entry := range ollama.Accounts {
		switch {
		case entry.Label == "work":
			healthy = entry.OK && entry.State == "healthy" && entry.ObservedAt.Equal(time.UnixMilli(observed).UTC())
		case entry.Label == "broken":
			invalid = entry.State == "invalid" && !entry.OK
		}
	}
	if !healthy || !invalid {
		t.Fatalf("ollama readings = %+v", ollama.Accounts)
	}
}

// TestFeishuAccountReportStateFromSignals pins the state mapping: a cached
// unavailable flag is never an auth failure, the enum-only collection signal is the
// only auth evidence, a fresh auth failure alerts, a stale one does not, and a
// successful reading observed after the auth failure is the only thing that
// recovers it.
func TestFeishuAccountReportStateFromSignals(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	healthySnapshot := repository.QuotaSnapshotRecord{
		AuthIndex: "x", Provider: "codex", Status: "healthy", ObservedAtMS: now.UnixMilli(),
		WindowsJSON: `[{"id":"weekly","scope":"standard","used_percent":10}]`,
	}

	// A cached unavailable flag is never an auth failure: it reads stale, fresh or old.
	for _, synced := range []time.Time{now.Add(-time.Minute), now.Add(-time.Hour), {}} {
		account := feishuAccountReport(repository.FeishuCredentialMetadata{
			AuthIndex: "x", Unavailable: true, ObservedAt: synced,
		}, repository.QuotaSnapshotRecord{}, repository.FeishuAccountSignal{}, now)
		if account.State != feishu.StateStale || account.AuthFailed {
			t.Fatalf("unavailable synced %s must read stale, not auth: %+v", synced, account)
		}
	}

	// A fresh auth signal with no valid reading is a credential problem.
	fresh := feishuAccountReport(repository.FeishuCredentialMetadata{AuthIndex: "x"},
		repository.QuotaSnapshotRecord{},
		repository.FeishuAccountSignal{State: repository.FeishuSignalAuthInvalid, ObservedAt: now.Add(-time.Minute)}, now)
	if fresh.State != feishu.StateInvalid || !fresh.AuthFailed {
		t.Fatalf("a fresh auth signal must read invalid, got %+v", fresh)
	}

	// A stale auth signal does not alert and does not recover.
	stale := feishuAccountReport(repository.FeishuCredentialMetadata{AuthIndex: "x"},
		repository.QuotaSnapshotRecord{},
		repository.FeishuAccountSignal{State: repository.FeishuSignalAuthInvalid, ObservedAt: now.Add(-time.Hour)}, now)
	if stale.State != feishu.StateStale || !stale.AuthFailed {
		t.Fatalf("a stale auth signal must read stale with AuthFailed set, got %+v", stale)
	}

	// An unknown-time auth signal is never fresh.
	unknown := feishuAccountReport(repository.FeishuCredentialMetadata{AuthIndex: "x"},
		repository.QuotaSnapshotRecord{},
		repository.FeishuAccountSignal{State: repository.FeishuSignalAuthInvalid}, now)
	if unknown.State != feishu.StateStale || !unknown.AuthFailed {
		t.Fatalf("an untimed auth signal must read stale, got %+v", unknown)
	}

	// A transport signal is not auth, and without a reading it reads stale.
	transport := feishuAccountReport(repository.FeishuCredentialMetadata{AuthIndex: "x"},
		repository.QuotaSnapshotRecord{},
		repository.FeishuAccountSignal{State: repository.FeishuSignalTransport, ObservedAt: now.Add(-time.Minute)}, now)
	if transport.State != feishu.StateStale || transport.AuthFailed {
		t.Fatalf("a transport signal must not read auth, got %+v", transport)
	}

	// A successful reading observed after the auth failure is positive recovery.
	recovered := feishuAccountReport(repository.FeishuCredentialMetadata{AuthIndex: "x"},
		healthySnapshot,
		repository.FeishuAccountSignal{State: repository.FeishuSignalAuthInvalid, ObservedAt: now.Add(-time.Minute)}, now)
	if recovered.State != feishu.StateHealthy || !recovered.OK || recovered.AuthFailed {
		t.Fatalf("a success after the auth failure must recover, got %+v", recovered)
	}

	// A success older than a fresh auth failure does not recover it.
	olderSuccess := healthySnapshot
	olderSuccess.ObservedAtMS = now.Add(-2 * time.Hour).UnixMilli()
	refuted := feishuAccountReport(repository.FeishuCredentialMetadata{AuthIndex: "x"},
		olderSuccess,
		repository.FeishuAccountSignal{State: repository.FeishuSignalAuthInvalid, ObservedAt: now.Add(-time.Minute)}, now)
	if refuted.State != feishu.StateInvalid || !refuted.AuthFailed {
		t.Fatalf("an old success must not recover a fresh auth failure, got %+v", refuted)
	}

	// A healthy signal without a stored reading is a known-healthy channel.
	healthySignal := feishuAccountReport(repository.FeishuCredentialMetadata{AuthIndex: "x"},
		repository.QuotaSnapshotRecord{},
		repository.FeishuAccountSignal{State: repository.FeishuSignalHealthy, ObservedAt: now.Add(-time.Minute)}, now)
	if healthySignal.State != feishu.StateHealthy || healthySignal.AuthFailed {
		t.Fatalf("a healthy signal must read healthy, got %+v", healthySignal)
	}
}

// TestFeishuReportSourceUsesStoredAuthSignal pins that the report reads a stored,
// enum-only auth signal as credential_invalid, with zero management requests.
func TestFeishuReportSourceUsesStoredAuthSignal(t *testing.T) {
	reference := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	var managementCalls atomic.Int64
	var handler *Handler
	_, _, repo := startDashboardTestServer(t, func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/v8/management") {
			managementCalls.Add(1)
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = writer.Write([]byte(`{}`))
	}, func(h *Handler) {
		handler = h
		h.nowFn = func() time.Time { return reference }
	})
	seedAuthFile(t, repo, "idx-codex", "codex.json", "codex", false, false, reference.Add(-time.Hour))
	if err := repo.SaveFeishuAccountSignal(context.Background(), repository.FeishuAccountSignal{
		AuthIndex: "idx-codex", Provider: "codex", State: repository.FeishuSignalAuthInvalid, ObservedAt: reference.Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	report, has := handler.BuildFeishuReportSource().ReadReport(context.Background())
	if !has {
		t.Fatal("a known channel must be available")
	}
	if calls := managementCalls.Load(); calls != 0 {
		t.Fatalf("a stored-only report made %d management requests, want 0", calls)
	}
	codex := reportProvider(t, report, "codex")
	if len(codex.Accounts) != 1 {
		t.Fatalf("codex accounts = %+v", codex.Accounts)
	}
	if account := codex.Accounts[0]; account.State != feishu.StateInvalid || !account.AuthFailed {
		t.Fatalf("a fresh stored auth signal must read invalid, got %+v", account)
	}
}

// TestFeishuReportSourceStaleAuthSignalDoesNotAlert pins that a stale auth signal
// is not a new alert, and does not read as recovered either.
func TestFeishuReportSourceStaleAuthSignalDoesNotAlert(t *testing.T) {
	reference := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	var handler *Handler
	_, _, repo := startDashboardTestServer(t, func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte(`{}`))
	}, func(h *Handler) {
		handler = h
		h.nowFn = func() time.Time { return reference }
	})
	seedAuthFile(t, repo, "idx-codex", "codex.json", "codex", false, false, reference.Add(-time.Hour))
	if err := repo.SaveFeishuAccountSignal(context.Background(), repository.FeishuAccountSignal{
		AuthIndex: "idx-codex", Provider: "codex", State: repository.FeishuSignalAuthInvalid, ObservedAt: reference.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}

	report, _ := handler.BuildFeishuReportSource().ReadReport(context.Background())
	codex := reportProvider(t, report, "codex")
	if account := codex.Accounts[0]; account.State != feishu.StateStale || !account.AuthFailed {
		t.Fatalf("a stale auth signal must read stale with AuthFailed, got %+v", account)
	}
}

// TestFeishuReportSourceAuthFailureFromRealRefresh drives the bounded fix from a
// real HTTP 401 through the actual refresh and persist path: a 401 from CPA's
// api-call proxy becomes a normalized needs_reauth recommendation, is stored as the
// safe auth signal, and is then read back as credential_invalid.
func TestFeishuReportSourceAuthFailureFromRealRefresh(t *testing.T) {
	reference := time.Now().UTC()
	var managementCalls atomic.Int64
	var handler *Handler
	client, base, repo := startDashboardTestServer(t, func(writer http.ResponseWriter, request *http.Request) {
		managementCalls.Add(1)
		switch {
		case strings.HasPrefix(request.URL.Path, "/v8/management/credentials"):
			_, _ = writer.Write([]byte(`{"files":[{"id":"file-1","name":"codex-main.json","auth_index":"codex-idx-1","type":"codex","provider":"codex","disabled":false}]}`))
		case request.URL.Path == "/v8/management/requests/api-call":
			var payload struct {
				URL string `json:"url"`
			}
			_ = json.NewDecoder(request.Body).Decode(&payload)
			if strings.Contains(payload.URL, "backend-api/wham/usage") {
				_, _ = writer.Write([]byte(`{"status_code":401,"body":{"message":"unauthorized"}}`))
				return
			}
			_, _ = writer.Write([]byte(`{"status_code":200,"body":{}}`))
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}, func(h *Handler) {
		handler = h
		h.nowFn = func() time.Time { return reference }
	})
	seedAuthFile(t, repo, "codex-idx-1", "codex-main.json", "codex", false, false, reference)

	request, err := http.NewRequest(http.MethodPost, base+"/omc/api/v1/management/quota/refresh", strings.NewReader(`{"auth_index":"codex-idx-1"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("refresh status = %d", response.StatusCode)
	}
	if managementCalls.Load() == 0 {
		t.Fatal("the real refresh must have contacted CPA")
	}

	signals, err := repo.GetFeishuAccountSignals(context.Background(), []string{"codex-idx-1"})
	if err != nil {
		t.Fatal(err)
	}
	signal, ok := signals["codex-idx-1"]
	if !ok || signal.State != repository.FeishuSignalAuthInvalid {
		t.Fatalf("a 401 refresh must store the auth signal, got %+v (present=%v)", signal, ok)
	}

	// The read itself must be fully stored-only.
	managementCalls.Store(0)
	report, _ := handler.BuildFeishuReportSource().ReadReport(context.Background())
	if reads := managementCalls.Load(); reads != 0 {
		t.Fatalf("a stored-only read made %d management requests, want 0", reads)
	}
	codex := reportProvider(t, report, "codex")
	if len(codex.Accounts) != 1 || codex.Accounts[0].State != feishu.StateInvalid {
		t.Fatalf("a real 401 must reach the report as invalid, got %+v", codex.Accounts)
	}
}

func reportProvider(t *testing.T, report feishu.Report, provider string) feishu.ProviderReport {
	t.Helper()
	for _, p := range report.Providers {
		if p.Provider == provider {
			return p
		}
	}
	t.Fatalf("provider %q not in report %+v", provider, report.Providers)
	return feishu.ProviderReport{}
}

func itoa(value int64) string {
	return strconv.FormatInt(value, 10)
}

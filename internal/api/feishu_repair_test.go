package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/feishu"
	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

func TestFeishuFixedThresholdRejectsConflictsAndKeepsPreferences(t *testing.T) {
	_, repo, _, server := newFeishuAPITest(t)
	put := func(payload string) (int, []byte) {
		t.Helper()
		request, _ := http.NewRequest(http.MethodPut, server.URL+"/feishu/settings", strings.NewReader(payload))
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := readAll(response)
		return response.StatusCode, body
	}
	if code, body := put(`{"tight_remaining_percent":20,"quiet_start":"22:00"}`); code != 400 || !bytes.Contains(body, []byte("read-only")) {
		t.Fatalf("code=%d body=%s", code, body)
	}
	if _, found, err := repo.LoadFeishuSettings(context.Background()); err != nil || found {
		t.Fatalf("rejected request persisted: found=%v err=%v", found, err)
	}
	if code, body := put(`{"tight_remaining_percent":10,"quiet_start":"22:00","quiet_end":"07:30","summary_times":["07:00"],"channel_unavailable_after_ms":120000,"monitor_down_after_ms":240000}`); code != 200 {
		t.Fatalf("code=%d body=%s", code, body)
	}
	saved, _, err := repo.LoadFeishuSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if saved.TightRemainingPercent != 10 || saved.QuietStart != "22:00" || saved.QuietEnd != "07:30" || saved.SummaryTimesJSON != `["07:00"]` || saved.ChannelUnavailableAfter != 120000 || saved.MonitorDownAfterMS != 240000 {
		t.Fatalf("preferences=%+v", saved)
	}
}

func TestFeishuPredictionSourceIncludesRecoveryAndResetProvenance(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	var handler *Handler
	_, _, repo := startDashboardTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("unexpected upstream request")
		w.WriteHeader(500)
	}, func(h *Handler) { handler = h; h.nowFn = func() time.Time { return now } })
	seedAuthFile(t, repo, "synthetic-prediction", "Synthetic", "codex", false, false, now)
	reset := now.Add(4 * time.Hour).UnixMilli()
	period := 10.0
	var windows []quota.QuotaWindow
	for i, used := range []float64{10, 15, 20} {
		at := now.Add(time.Duration(i-2) * 150 * time.Minute).UnixMilli()
		windows = []quota.QuotaWindow{{ID: "7d", Kind: "weekly", Scope: "standard", UsedPercent: &used, ResetAtMS: &reset, PeriodHours: &period, ResetAccuracy: "exact"}}
		if _, err := repo.RecordActiveQuotaObservations(context.Background(), &quota.NormalizedQuota{AuthIndex: "synthetic-prediction", Provider: "codex", Status: "healthy", ObservedAtMS: at, Windows: windows}); err != nil {
			t.Fatal(err)
		}
	}
	encoded, _ := json.Marshal(windows)
	if err := repo.SaveQuotaSnapshot(context.Background(), repository.QuotaSnapshotRecord{AuthIndex: "synthetic-prediction", Provider: "codex", Status: "healthy", ObservedAtMS: now.UnixMilli(), WindowsJSON: string(encoded)}); err != nil {
		t.Fatal(err)
	}
	api, err := handler.BuildFeishuAPI(context.Background(), handler.BuildFeishuReportSource(), feishu.Overlay{})
	if err != nil {
		t.Fatal(err)
	}
	digest, err := api.readPrediction(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(digest.Items) != 1 || (digest.Items[0].Verdict != "under_used" && digest.Items[0].Verdict != "on_track") || digest.Items[0].ResetAccuracy != "exact" || digest.Items[0].WindowID != "7d" {
		t.Fatalf("production recovery projection=%+v", digest)
	}
}

func TestFeishuDerivedQuotaDeduplicatesThroughSQLiteAndServiceRestart(t *testing.T) {
	clock := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	var handler *Handler
	_, _, repo := startDashboardTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("unexpected upstream request")
		w.WriteHeader(500)
	}, func(h *Handler) { handler = h; h.nowFn = func() time.Time { return clock } })
	seedAuthFile(t, repo, "synthetic-moving", "Synthetic", "codex", false, false, clock)
	sender := &monitorSender{}
	secret := "synthetic-secret"
	for i := 0; i < 5; i++ {
		clock = clock.Add(time.Minute)
		reset := clock.Add(5 * time.Hour).UnixMilli()
		used := 93.0
		windows, _ := json.Marshal([]quota.QuotaWindow{{ID: "7d", Kind: "weekly", Scope: "standard", UsedPercent: &used, ResetAtMS: &reset, ResetAccuracy: "derived"}})
		if err := repo.SaveQuotaSnapshot(context.Background(), repository.QuotaSnapshotRecord{AuthIndex: "synthetic-moving", Provider: "codex", Status: "healthy", ObservedAtMS: clock.UnixMilli(), WindowsJSON: string(windows)}); err != nil {
			t.Fatal(err)
		}
		store := monitorStore{feishuStore: feishuStore{repo: repo}, saves: make(chan struct{}, 2)}
		service := feishu.NewService(feishu.Options{Web: feishu.Config{Enabled: true, AppID: "synthetic", ChatID: "ou_synthetic", Timezone: "UTC"}, Overlay: feishu.Overlay{AppSecret: &secret}, Source: handler.BuildFeishuReportSource(), Store: store, Now: func() time.Time { return clock }, SenderFactory: func(feishu.Resolved) feishu.Sender { return sender }, SourceFactory: func(feishu.Resolved) feishu.EventSource { return monitorEvents{} }, EvalInterval: time.Hour})
		if err := service.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		deadline := time.NewTimer(5 * time.Second)
		for n := 0; n < 2; n++ {
			select {
			case <-store.saves:
			case <-deadline.C:
				service.Stop()
				t.Fatal("startup did not persist")
			}
		}
		deadline.Stop()
		service.Stop()
		if sends := sender.sends.Load(); sends != 1 {
			t.Fatalf("restart %d sends=%d", i, sends)
		}
	}
}

func TestFeishuTestRouteReportsPersistedRetry(t *testing.T) {
	api, repo, _, _ := newFeishuAPITest(t)
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	secret := "synthetic-secret"
	sender := &apiPredictionSender{err: errors.New("synthetic failure")}
	api.service = feishu.NewService(feishu.Options{Web: feishu.Config{Enabled: true, AppID: "synthetic", ChatID: "ou_synthetic"}, Overlay: feishu.Overlay{AppSecret: &secret}, Store: feishuStore{repo: repo}, Now: func() time.Time { return now }, SenderFactory: func(feishu.Resolved) feishu.Sender { return sender }})
	response := httptest.NewRecorder()
	api.postTest(response, httptest.NewRequest(http.MethodPost, "/feishu/test", nil))
	var result feishuTestResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || result.Sent || result.Error != "" || result.Reason != "queued_for_retry" || result.Attempts != 1 || result.NextAttemptAt == nil || !result.NextAttemptAt.After(now) {
		t.Fatalf("response=%s", response.Body.String())
	}
	runtime, err := repo.LoadFeishuRuntime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(runtime.Notifications) != 1 || runtime.Notifications[0].State != feishu.NotifyPending || runtime.Notifications[0].Attempts != 1 {
		t.Fatalf("retry lost: %+v", runtime)
	}
}

type failedFeishuAttemptStore struct {
	feishu.Store
	saves int
}

func (store *failedFeishuAttemptStore) Save(ctx context.Context, state *feishu.State) error {
	store.saves++
	if store.saves == 2 {
		return errors.New("synthetic attempt save failure")
	}
	return store.Store.Save(ctx, state)
}

func TestFeishuTestRouteReportsAttemptPersistenceFailure(t *testing.T) {
	api, repo, _, _ := newFeishuAPITest(t)
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	secret := "synthetic-secret"
	sender := &apiPredictionSender{err: errors.New("synthetic transport failure")}
	store := &failedFeishuAttemptStore{Store: feishuStore{repo: repo}}
	api.service = feishu.NewService(feishu.Options{Web: feishu.Config{Enabled: true, AppID: "synthetic", ChatID: "ou_synthetic"}, Overlay: feishu.Overlay{AppSecret: &secret}, Store: store, Now: func() time.Time { return now }, SenderFactory: func(feishu.Resolved) feishu.Sender { return sender }})
	response := httptest.NewRecorder()
	api.postTest(response, httptest.NewRequest(http.MethodPost, "/feishu/test", nil))
	var result feishuTestResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || result.Sent || result.Error != "synthetic attempt save failure" || result.Reason != "" || result.Attempts != 0 || result.NextAttemptAt != nil {
		t.Fatalf("response=%s", response.Body.String())
	}
	state, err := store.Load(context.Background())
	if err != nil || len(state.Outbox) != 1 || state.Outbox[0].Attempts != 0 || !state.Outbox[0].NextAttemptAt.IsZero() {
		t.Fatalf("initial notification not preserved: state=%+v error=%v", state, err)
	}
}

func TestFeishuWindowProvenanceAndCycleRoundTrip(t *testing.T) {
	_, repo, _, _ := newFeishuAPITest(t)
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	reset := now.Add(time.Hour).UnixMilli()
	used := 93.0
	windows := feishuWindowsFromQuotaWindows([]quota.QuotaWindow{{ID: "synthetic-week", Kind: "weekly", Scope: "standard", UsedPercent: &used, ResetAtMS: &reset, ResetAccuracy: "derived"}})
	if len(windows) != 1 || windows[0].ID != "synthetic-week" || windows[0].Kind != "weekly" || windows[0].ResetAccuracy != "derived" || windows[0].ResetAt == nil {
		t.Fatalf("projection=%+v", windows)
	}
	store := feishuStore{repo: repo}
	state := feishu.EmptyState()
	state.PendingItems = []feishu.PendingItem{{ID: "synthetic-id", Kind: feishu.AlertQuotaLow, Status: feishu.PendingResolved, Since: now, NotifiedAt: now, ResolvedAt: now.Add(time.Minute), CycleResetAt: time.UnixMilli(reset).UTC()}}
	if err := store.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.PendingItems) != 1 || !loaded.PendingItems[0].CycleResetAt.Equal(state.PendingItems[0].CycleResetAt) || !loaded.PendingItems[0].ResolvedAt.Equal(state.PendingItems[0].ResolvedAt) {
		t.Fatalf("cycle lost: %+v", loaded)
	}
}

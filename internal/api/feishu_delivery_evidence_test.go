package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/feishu"
	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

func TestFeishuLegacyNonAuthSignalRetainsItsOwnMonitoringClock(t *testing.T) {
	now := time.Date(2026, 11, 6, 2, 0, 0, 0, time.UTC)
	for _, state := range []string{repository.FeishuSignalTransport, repository.FeishuSignalParse, repository.FeishuSignalUnknown, repository.FeishuSignalAuthInvalid} {
		account := feishuAccountReport(repository.FeishuCredentialMetadata{AuthIndex: "synthetic", Driver: "codex"}, repository.QuotaSnapshotRecord{}, repository.FeishuAccountSignal{State: state, ObservedAt: now}, now)
		if !account.AttemptedAt.IsZero() || !account.ObservedAt.IsZero() {
			t.Fatalf("signal invented a latest attempt or numeric observation: %+v", account)
		}
		if state == repository.FeishuSignalAuthInvalid {
			if !account.CollectionSignalObservedAt.IsZero() || !account.AuthSignalObservedAt.Equal(now) {
				t.Fatalf("authentication latch became a collection failure event: %+v", account)
			}
		} else if !account.CollectionFailed || !account.CollectionSignalObservedAt.Equal(now) {
			t.Fatalf("non-auth monitoring event lost its real clock: %+v", account)
		}
	}
}

func TestFeishuOllamaAttemptCannotSupplyAnUnknownQuotaObservation(t *testing.T) {
	now := time.Date(2026, 11, 6, 2, 0, 0, 0, time.UTC)
	attemptMS := now.UnixMilli()
	account := ollamaAccountReport(repository.OllamaAccount{ID: "synthetic", Enabled: true, Status: "auth_invalid", LastObservedAtMS: &attemptMS})
	if !account.AttemptedAt.Equal(now) || !account.AuthSignalObservedAt.Equal(now) || !account.ObservedAt.IsZero() || !account.LastSuccessAt.IsZero() {
		t.Fatalf("attempt/authentication time became an invented quota observation: %+v", account)
	}
	quotaMS := now.Add(-17 * 24 * time.Hour).UnixMilli()
	account = ollamaAccountReport(repository.OllamaAccount{ID: "synthetic", Enabled: true, Status: "auth_invalid", LastObservedAtMS: &attemptMS, QuotaObservedAtMS: &quotaMS})
	if !account.ObservedAt.Equal(time.UnixMilli(quotaMS)) || !account.AuthSignalObservedAt.Equal(now) {
		t.Fatalf("independent stored quota/authentication clocks were conflated: %+v", account)
	}
}

func TestFeishuPredictionDeliveryReceivesTheAccountingCadenceBound(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	var handler *Handler
	_, _, repo := startDashboardTestServer(t, func(writer http.ResponseWriter, _ *http.Request) {
		t.Error("prediction contacted an upstream service")
		writer.WriteHeader(500)
	}, func(current *Handler) { handler = current; current.nowFn = func() time.Time { return now } })
	seedAuthFile(t, repo, "synthetic-bound", "Synthetic", "codex", false, false, now)
	period, used, reset := 24.0, 100.0, now.Add(6*time.Hour).UnixMilli()
	windows, err := json.Marshal([]quota.QuotaWindow{{ID: "weekly", Kind: "weekly", Scope: "standard", UsedPercent: &used, ResetAtMS: &reset, PeriodHours: &period, ResetAccuracy: "exact"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveQuotaSnapshot(context.Background(), repository.QuotaSnapshotRecord{AuthIndex: "synthetic-bound", Provider: "codex", Status: "healthy", ObservedAtMS: now.UnixMilli(), WindowsJSON: string(windows)}); err != nil {
		t.Fatal(err)
	}
	api, err := handler.BuildFeishuAPI(context.Background(), handler.BuildFeishuReportSource(), feishu.Overlay{})
	if err != nil {
		t.Fatal(err)
	}
	digest, err := api.readPrediction(context.Background())
	if err != nil || len(digest.Items) != 1 {
		t.Fatalf("prediction digest=%+v err=%v", digest, err)
	}
	item := digest.Items[0]
	if item.Verdict != "will_exhaust" || item.FreshWindowMS != (6*time.Hour).Milliseconds() || item.ObservedAtMS != now.UnixMilli() {
		t.Fatalf("prediction lost its original evidence cadence in transit: %+v", item)
	}
}

func TestFeishuDeliveryEvidenceAndSuppressionSurviveSQLiteReload(t *testing.T) {
	_, repo, _, _ := newFeishuAPITest(t)
	store := feishuStore{repo: repo}
	state := feishu.EmptyState()
	now := time.Date(2026, 11, 6, 2, 0, 0, 0, time.UTC)
	state.Outbox = []feishu.Notification{{ID: "synthetic", Kind: feishu.AlertReadingMissing, State: feishu.NotifySuppressed, CreatedAt: now,
		Alerts: []feishu.Alert{{Kind: feishu.AlertReadingMissing, Provider: "codex", Key: "synthetic", ObservedAt: now.Add(-time.Minute), EvidenceSource: feishu.EvidenceAttempt, FreshWindowMS: time.Minute.Milliseconds()}}}}
	if err := store.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Outbox) != 1 || loaded.Outbox[0].State != feishu.NotifySuppressed || !loaded.Outbox[0].SentAt.IsZero() || len(loaded.Outbox[0].Alerts) != 1 {
		t.Fatalf("terminal suppression lost or acknowledged: %+v", loaded)
	}
	alert := loaded.Outbox[0].Alerts[0]
	if alert.EvidenceSource != feishu.EvidenceAttempt || alert.FreshWindowMS != time.Minute.Milliseconds() || !alert.ObservedAt.Equal(now.Add(-time.Minute)) {
		t.Fatalf("delivery evidence was not preserved: %+v", alert)
	}
}

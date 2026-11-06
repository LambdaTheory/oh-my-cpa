package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/feishu"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

func TestFeishuHistoricalAuthAndLatestAttemptHaveIndependentClocks(t *testing.T) {
	now := time.Date(2026, 11, 6, 2, 0, 0, 0, time.UTC)
	oldReading := now.Add(-17 * 24 * time.Hour)
	oldAuth := oldReading.Add(time.Minute)
	ctx := context.Background()
	var managementCalls atomic.Int64
	var handler *Handler
	_, _, repo := startDashboardTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		managementCalls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}, func(h *Handler) {
		handler = h
		h.nowFn = func() time.Time { return now }
	})
	seedAuthFile(t, repo, "synthetic", "synthetic.json", "antigravity", false, false, now)
	if err := repo.SaveQuotaSnapshot(ctx, repository.QuotaSnapshotRecord{
		AuthIndex: "synthetic", Provider: "antigravity", Status: "healthy", ObservedAtMS: oldReading.UnixMilli(),
		WindowsJSON: `[{"id":"weekly","scope":"standard","used_percent":95}]`,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveFeishuAccountSignal(ctx, repository.FeishuAccountSignal{
		AuthIndex: "synthetic", Provider: "antigravity", State: repository.FeishuSignalAuthInvalid, ObservedAt: oldAuth,
	}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ signal, reason string }{
		{repository.FeishuSignalTransport, "network"},
		{repository.FeishuSignalParse, "parse"},
		{repository.FeishuSignalUnknown, "unknown"},
	} {
		t.Run(test.reason, func(t *testing.T) {
			if err := repo.SaveFeishuAccountSignal(ctx, repository.FeishuAccountSignal{
				AuthIndex: "synthetic", Provider: "antigravity", State: test.signal, ObservedAt: now,
			}); err != nil {
				t.Fatal(err)
			}
			if err := repo.SaveQuotaRefreshAttempt(ctx, repository.QuotaRefreshAttempt{
				AuthIndex: "synthetic", Outcome: "failed", AttemptedAtMS: now.UnixMilli(), Reason: test.reason,
				ReasonSummary: "synthetic-upstream-detail-must-not-enter-notifications",
			}); err != nil {
				t.Fatal(err)
			}
			report, has := handler.BuildFeishuReportSource().ReadReport(ctx)
			if !has || managementCalls.Load() != 0 {
				t.Fatal("report must read stored evidence without contacting CPA")
			}
			account := reportProvider(t, report, "antigravity").Accounts[0]
			if !account.ObservedAt.Equal(oldReading) || !account.LastSuccessAt.Equal(oldReading) || !account.AuthSignalObservedAt.Equal(oldAuth) || !account.AttemptedAt.Equal(now) {
				t.Fatalf("evidence clocks conflated: %+v", account)
			}
			if !account.AuthFailed || account.HasFreshAuthFailure(now) || account.State != feishu.StateStale {
				t.Fatalf("historical authentication must remain unresolved, not current: %+v", account)
			}
			if !account.CollectionFailed || account.AttemptOutcome != "failed" || account.AttemptReason != test.reason {
				t.Fatalf("latest non-auth collection result was hidden by the old latch: %+v", account)
			}
			encoded, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), "synthetic-upstream-detail") {
				t.Fatal("raw attempt summaries must not enter the notification DTO")
			}
		})
	}
}

func TestFeishuSnapshotAuthenticationAgeAndUnknownAttempt(t *testing.T) {
	now := time.Date(2026, 11, 6, 2, 0, 0, 0, time.UTC)
	for _, age := range []time.Duration{time.Minute, 17 * 24 * time.Hour} {
		observed := now.Add(-age)
		account := feishuAccountReport(repository.FeishuCredentialMetadata{AuthIndex: "synthetic"}, repository.QuotaSnapshotRecord{
			AuthIndex: "synthetic", Status: "needs_reauth", ObservedAtMS: observed.UnixMilli(),
		}, repository.FeishuAccountSignal{}, now)
		if !account.AuthFailed || !account.AuthSignalObservedAt.Equal(observed) || !account.AttemptedAt.IsZero() {
			t.Fatalf("snapshot auth evidence must not invent a latest attempt: %+v", account)
		}
		if got, want := account.HasFreshAuthFailure(now), age <= feishu.DefaultFreshWindow; got != want {
			t.Fatalf("auth age %s: fresh=%v want=%v", age, got, want)
		}
		if age > feishu.DefaultFreshWindow && account.State != feishu.StateStale {
			t.Fatalf("old snapshot must not remain currently invalid: %+v", account)
		}
	}
}

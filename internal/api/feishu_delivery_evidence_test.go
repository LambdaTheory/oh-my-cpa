package api

import (
	"context"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/feishu"
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

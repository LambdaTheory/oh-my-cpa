package feishu

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestPredictionDeliveryClearsOmittedWhenOnlyOtherAlertsSurvive(t *testing.T) {
	for _, shouldRecheck := range []bool{false, true} {
		name := "freshness"
		if shouldRecheck {
			name = "current_roster"
		}
		t.Run(name, func(t *testing.T) {
			now := mustTime("2026-10-27T09:00:00Z")
			sender := newFakeSender()
			dispatcher := NewDispatcher(sender, NewPlainReportRenderer(time.UTC), nil).withClock(func() time.Time { return now })
			if shouldRecheck {
				dispatcher.predictionSource = func(context.Context) (PredictionDigest, error) { return PredictionDigest{}, nil }
			}
			notification := predictionNotification([]PredictionDigestItem{{
				CredentialLabel: "removed", Key: "gone", Provider: "codex", WindowKind: "weekly", WindowID: "weekly",
				LastsUntilReset: &cannotLast, ObservedAtMS: now.Add(-3 * time.Hour).UnixMilli(), FreshWindowMS: time.Hour.Milliseconds(),
			}}, 3, readyPredictionConfig(), now)
			notification.Alerts = append(notification.Alerts, Alert{Kind: AlertChannelUnavailable, Provider: "claude", Title: "渠道不可用", ObservedAt: now, EvidenceSource: EvidenceAvailability, FreshWindowMS: defaultFreshWindow.Milliseconds()})

			out := dispatcher.deliverDue(context.Background(), "chat", []Notification{notification})
			if sends, _ := sender.counts(); sends != 1 || out[0].State != NotifySent {
				t.Fatalf("other alert was not delivered: sends=%d out=%+v", sends, out)
			}
			payload := marshalTestJSON(sender.lastCard())
			if !strings.Contains(payload, "渠道不可用") || !strings.Contains(payload, "其他需要处理") {
				t.Fatalf("other alert lost: %s", payload)
			}
			if strings.Contains(payload, "撑不到重置") || strings.Contains(payload, "另有 3 项") || strings.Contains(payload, "removed") || out[0].Omitted != 0 {
				t.Fatalf("discarded prediction left an omitted count: out=%+v card=%s", out, payload)
			}
		})
	}
}

package feishu

import (
	"testing"
	"time"
)

func TestQueueNotificationDeduplicatesByID(t *testing.T) {
	state := EmptyState()
	state.queueNotification(Notification{ID: "n1", State: NotifyPending, Attempts: 2})
	state.queueNotification(Notification{ID: "n1", State: NotifyPending})
	if len(state.Outbox) != 1 {
		t.Fatalf("expected one outbox row, got %d", len(state.Outbox))
	}
	if state.Outbox[0].Attempts != 2 {
		t.Fatalf("re-queueing must preserve delivery progress, got attempts=%d", state.Outbox[0].Attempts)
	}
}

func TestTrimOutboxDropsOldestSentFirst(t *testing.T) {
	state := EmptyState()
	for i := 0; i < maxOutboxPerChannel+10; i++ {
		state.queueNotification(Notification{ID: "sent" + string(rune('a'+i%26)) + string(rune('0'+i/26)), State: NotifySent, SentAt: time.Now()})
	}
	state.queueNotification(Notification{ID: "pending", State: NotifyPending})
	if len(state.Outbox) > maxOutboxPerChannel {
		t.Fatalf("outbox not bounded: %d", len(state.Outbox))
	}
	found := false
	for _, notification := range state.Outbox {
		if notification.ID == "pending" {
			found = true
		}
	}
	if !found {
		t.Fatal("a pending notification must never be dropped")
	}
}

func TestTakeQueuedPromotesToPending(t *testing.T) {
	state := EmptyState()
	state.queueNotification(Notification{ID: "q1", State: NotifyQueued})
	got := state.takeQueued()
	if len(got) != 1 {
		t.Fatalf("expected one queued notification, got %d", len(got))
	}
	if state.Outbox[0].State != NotifyPending {
		t.Fatalf("queued notification should become pending, got %s", state.Outbox[0].State)
	}
}

func TestPruneDropsOldSentAndResolvedOnly(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	state := EmptyState()
	state.PendingItems = []PendingItem{
		{ID: "open", Status: PendingOpen},
		{ID: "resolved-old", Status: PendingResolved, ResolvedAt: now.Add(-8 * 24 * time.Hour)},
		{ID: "resolved-new", Status: PendingResolved, ResolvedAt: now.Add(-time.Hour)},
	}
	state.Outbox = []Notification{
		{ID: "sent-old", State: NotifySent, SentAt: now.Add(-48 * time.Hour)},
		{ID: "sent-new", State: NotifySent, SentAt: now.Add(-time.Hour)},
		{ID: "pending", State: NotifyPending},
	}
	state.prune(now)
	if len(state.PendingItems) != 2 {
		t.Fatalf("expected open + recent resolved, got %d", len(state.PendingItems))
	}
	if len(state.Outbox) != 2 {
		t.Fatalf("expected recent sent + pending, got %d", len(state.Outbox))
	}
}

func TestCloneStateIsIndependent(t *testing.T) {
	state := EmptyState()
	state.Outbox = append(state.Outbox, Notification{ID: "n1", Alerts: []Alert{{Facts: []string{"f"}}}})
	clone := cloneState(state)
	clone.Outbox[0].Alerts[0].Facts[0] = "changed"
	if state.Outbox[0].Alerts[0].Facts[0] != "f" {
		t.Fatal("clone must not share backing arrays with the source")
	}
}

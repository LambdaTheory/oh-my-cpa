package feishu

import (
	"context"
	"strings"
	"testing"
)

func TestAC04AC05AC10NotificationsFollowSavedRecipient(t *testing.T) {
	env := newTestEnv(t, Report{}, false)
	for _, recipient := range []string{"oc_synthetic", "ou_synthetic"} {
		web, overlay := readyConfig()
		web.ChatID, web.ConfirmedRecipient = recipient, recipient
		env.service.Reload(context.Background(), web, overlay)
		delivery, err := env.service.SendTest(context.Background())
		if err != nil || !delivery.Sent || env.sender.lastChatID() != recipient {
			t.Fatalf("test delivery did not follow recipient %s: %+v %v", recipient, delivery, err)
		}
	}
}

func TestAC09DeliveryFailurePersistsRetryAndClearsOnSuccess(t *testing.T) {
	env := newTestEnv(t, Report{}, false)
	env.sender.failSends = 2
	delivery, err := env.service.SendTest(context.Background())
	if err != nil || !delivery.QueuedForRetry {
		t.Fatalf("delivery=%+v err=%v", delivery, err)
	}
	status := env.service.StatusSnapshot(context.Background())
	if status.PendingSends != 1 || status.LastError == "" || status.NextAttemptAt == nil {
		t.Fatal("failed delivery has no durable retry status")
	}
	*env.clock = *status.NextAttemptAt
	env.service.deliverOutbox(context.Background(), env.service.resolvedNow())
	status = env.service.StatusSnapshot(context.Background())
	if status.PendingSends != 0 || status.NextAttemptAt != nil || status.LastError != "" {
		t.Fatalf("successful retry did not clear failure: %+v", status)
	}
}

func TestAC09RecipientFailureGuidanceDoesNotEchoNativeMessage(t *testing.T) {
	for _, test := range []struct{ message, want string }{
		{"bot is not in chat synthetic-private-value", "把机器人拉进群"},
		{"outside availability scope synthetic-private-value", "应用的可用范围"},
		{"chat does not exist synthetic-private-value", "检查号码"},
		{"unknown synthetic-private-value", "检查接收人和应用权限"},
	} {
		err := recipientDeliveryError(123, test.message)
		if !strings.Contains(err.Error(), test.want) || strings.Contains(err.Error(), "synthetic-private-value") {
			t.Fatalf("guidance=%s", err)
		}
	}
}

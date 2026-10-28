package feishu

import (
	"context"
	"testing"
)

func TestAC08LegacyGroupSilentUntilExactConfirmation(t *testing.T) {
	web, overlay := readyConfig()
	web.ChatID = "oc_synthetic"
	sender := newFakeSender()
	source := &fakeReportSource{}
	bot := NewService(Options{Web: web, Overlay: overlay, Source: source, SenderFactory: func(Resolved) Sender { return sender }})
	t.Cleanup(bot.Stop)
	if !bot.resolvedNow().NeedsRecipientConfirmation || bot.resolvedNow().Ready() {
		t.Fatal("legacy group must require confirmation")
	}
	if _, err := bot.SendTest(context.Background()); err == nil {
		t.Fatal("unconfirmed group allowed a test send")
	}
	if err := bot.HandleMessage(context.Background(), Inbound{ChatID: "oc_synthetic", ChatType: "group", BotMentioned: true, MessageID: "synthetic-message", Text: "帮助"}); err != nil {
		t.Fatal(err)
	}
	if sends, replies := sender.counts(); sends != 0 || replies != 0 || source.readCount() != 0 {
		t.Fatal("unconfirmed recipient performed work")
	}
	web.ConfirmedRecipient = "oc_other"
	if resolveConfig(web, overlay).Ready() {
		t.Fatal("confirmation for another group authorized delivery")
	}
	web.ConfirmedRecipient = web.ChatID
	bot.Reload(context.Background(), web, overlay)
	if !bot.resolvedNow().Ready() || bot.resolvedNow().NeedsRecipientConfirmation {
		t.Fatal("exact confirmed group did not become ready")
	}
}

func TestAC01RecipientKinds(t *testing.T) {
	for value, want := range map[string]string{
		"oc_synthetic-group":  RecipientGroup,
		"ou_synthetic-person": RecipientPerson,
	} {
		if got := RecipientKind(value); got != want {
			t.Errorf("RecipientKind(%q) = %q, want %q", value, got, want)
		}
	}
}

func TestAC06AC07AC11HandlerAppliesRouting(t *testing.T) {
	for _, test := range []struct {
		name, recipient, chatType, sender string
		mentioned, want                   bool
	}{
		{"receiving group", "oc_target", "group", "ou_other", true, true},
		{"other group", "oc_target", "group", "ou_other", true, true},
		{"direct query", "oc_target", "p2p", "ou_other", false, true},
		{"unmentioned group", "oc_target", "group", "ou_other", false, false},
		{"own direct query", "ou_target", "p2p", "ou_target", false, true},
		{"other person", "ou_target", "p2p", "ou_other", false, false},
		{"personal recipient group", "ou_target", "group", "ou_target", true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			env := newTestEnv(t, Report{}, false)
			web, overlay := readyConfig()
			web.ChatID, web.ConfirmedRecipient = test.recipient, test.recipient
			env.service.Reload(context.Background(), web, overlay)
			t.Cleanup(env.service.Stop)
			if err := env.service.HandleMessage(context.Background(), Inbound{ChatID: "oc_other", ChatType: test.chatType, SenderOpenID: test.sender, BotMentioned: test.mentioned, MessageID: "synthetic-message", Text: "帮助"}); err != nil {
				t.Fatal(err)
			}
			env.service.taskGroup().wg.Wait()
			_, replies := env.sender.counts()
			if (replies == 1) != test.want || replies > 1 {
				t.Fatalf("replies=%d, want reply=%v", replies, test.want)
			}
		})
	}
}

func TestAC02InvalidRecipientCannotAuthorizeQueries(t *testing.T) {
	for _, value := range []string{"", "oc_", "ou_", "person@example.test", "123456", " oc_synthetic", "oc_synthetic/other", "ou_synthetic\n"} {
		if RecipientKind(value) != "" || canAnswerRecipient(value, Inbound{ChatType: "p2p", SenderOpenID: value}) {
			t.Errorf("invalid recipient %q was accepted", value)
		}
	}
}

func TestAC06AC07AC11RecipientQueryRouting(t *testing.T) {
	for _, test := range []struct {
		name      string
		recipient string
		in        Inbound
		want      bool
	}{
		{"group member mention", "oc_target", Inbound{ChatID: "oc_target", ChatType: "group", BotMentioned: true}, true},
		{"other group mention", "oc_target", Inbound{ChatID: "oc_other", ChatType: "group", BotMentioned: true}, true},
		{"group without mention", "oc_target", Inbound{ChatID: "oc_target", ChatType: "group"}, false},
		{"any private sender for group", "oc_target", Inbound{ChatType: "p2p", SenderOpenID: "ou_other"}, true},
		{"person own private chat", "ou_target", Inbound{ChatType: "p2p", SenderOpenID: "ou_target"}, true},
		{"person other private chat", "ou_target", Inbound{ChatType: "p2p", SenderOpenID: "ou_other"}, false},
		{"person group mention", "ou_target", Inbound{ChatType: "group", SenderOpenID: "ou_target", BotMentioned: true}, false},
		{"unknown conversation type", "oc_target", Inbound{ChatType: "unknown", BotMentioned: true}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := canAnswerRecipient(test.recipient, test.in); got != test.want {
				t.Fatalf("routing = %v, want %v", got, test.want)
			}
		})
	}
}

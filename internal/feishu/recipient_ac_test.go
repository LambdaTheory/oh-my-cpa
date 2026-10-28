package feishu

import "testing"

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

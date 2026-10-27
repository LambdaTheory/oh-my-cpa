package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/reauth"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

func TestOAuthMessageWhitelistAndCards(t *testing.T) {
	ctx := context.Background()
	db, err := repository.Open(ctx, fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name()))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := repository.New(db)
	sender := newFakeSender()
	var logs bytes.Buffer
	secret := "synthetic-app-secret"
	bot := NewService(Options{Web: Config{Enabled: true, AppID: "cli_synthetic", ChatID: "ou_synthetic"}, Overlay: Overlay{AppSecret: &secret}, SenderFactory: func(Resolved) Sender { return sender }, Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	now := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	service := reauth.New(reauth.Options{Repo: repo, Now: func() time.Time { return now }, Enabled: bot.OAuthReady, Notify: bot.SendOAuthCard})
	bot.ConfigureOAuth("oc_oauth", "ou_contact", service.AcceptCode)
	valid := Inbound{MessageID: "synthetic-msg", ChatID: "oc_oauth", ChatType: "group", SenderOpenID: "ou_contact", MessageType: "text", Text: "123456"}
	// No waiting task means even an otherwise valid code has no effect.
	if err := bot.HandleMessage(ctx, valid); err != nil {
		t.Fatal(err)
	}
	if err := service.Observe(ctx, "synthetic-auth", "claude", "oauth", "Synthetic Claude", "auth_invalid"); err != nil {
		t.Fatal(err)
	}
	j, err := service.Next(ctx, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Report(ctx, j.ID, "oauth_started", "", reauth.OAuthStart{}); err != nil {
		t.Fatal(err)
	}
	if err := service.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	card, _ := json.Marshal(sender.lastCard())
	var decoded map[string]any
	_ = json.Unmarshal(card, &decoded)
	elements := decoded["body"].(map[string]any)["elements"].([]any)
	markdown := elements[0].(map[string]any)["content"].(string)
	if !strings.Contains(markdown, `<at id_type="open_id" id="ou_contact">`) || !strings.Contains(markdown, "6 位验证码") || !strings.Contains(string(card), "Synthetic Claude") || sender.lastChatID() != "oc_oauth" {
		t.Fatalf("wrong request card: %s", card)
	}
	for _, mutate := range []func(*Inbound){
		func(in *Inbound) { in.ChatID = "oc_wrong" }, func(in *Inbound) { in.SenderOpenID = "ou_wrong" }, func(in *Inbound) { in.MessageType = "image" }, func(in *Inbound) { in.Text = "12345" }, func(in *Inbound) { in.Text = "1234567" }, func(in *Inbound) { in.FromBot = true }, func(in *Inbound) { in.MessageID = "" },
	} {
		in := valid
		mutate(&in)
		if err := bot.HandleMessage(ctx, in); err != nil {
			t.Fatal(err)
		}
		latest, _ := service.Get(ctx, j.ID)
		if latest.State != "waiting_code" {
			t.Fatal("whitelist bypass")
		}
	}
	if err := bot.HandleMessage(ctx, valid); err != nil {
		t.Fatal(err)
	}
	if err := bot.HandleMessage(ctx, valid); err != nil {
		t.Fatal(err)
	}
	if err := service.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	code, err := service.TakeCode(ctx, j.ID)
	if err != nil || code != valid.Text {
		t.Fatal("valid code not delivered")
	}
	if again, err := service.TakeCode(ctx, j.ID); err != nil || again != "" {
		t.Fatal("replayed code")
	}
	if sends, replies := sender.counts(); sends != 2 || replies != 0 {
		t.Fatalf("sends=%d replies=%d", sends, replies)
	}
	if strings.Contains(logs.String(), valid.Text) {
		t.Fatal("code logged")
	}
}

func TestOAuthSDKLoggerDoesNotRetainEventBodies(t *testing.T) {
	var output bytes.Buffer
	logger := eventLogger{logger: slog.New(slog.NewTextHandler(&output, nil))}
	for _, log := range []func(context.Context, ...interface{}){logger.Debug, logger.Info, logger.Warn, logger.Error} {
		log(context.Background(), "synthetic-code-body-123456")
	}
	if strings.Contains(output.String(), "123456") {
		t.Fatal("SDK body leaked")
	}
	if !strings.Contains(output.String(), "event transport failure") {
		t.Fatal("failure category missing")
	}
}

func TestOAuthConfigurationWithoutAlertGroup(t *testing.T) {
	secret := "synthetic-secret"
	bot := NewService(Options{Web: Config{Enabled: true, AppID: "cli_test"}, Overlay: Overlay{AppSecret: &secret, OAuthChatID: "oc_oauth"}, SenderFactory: func(Resolved) Sender { return newFakeSender() }})
	bot.ConfigureOAuth("oc_oauth", "ou_contact", nil)
	if !bot.OAuthReady() {
		t.Fatal("OAuth-only configuration inactive")
	}
	if bot.resolvedNow().Ready() {
		t.Fatal("alerts enabled without alert group")
	}
	bot.ConfigureOAuth("oc_oauth", "", nil)
	if bot.OAuthReady() {
		t.Fatal("missing contact enabled OAuth")
	}
}

func TestOAuthCardsFollowProviderFlow(t *testing.T) {
	for _, c := range []struct{ provider, want string }{
		{"codex", "6 位验证码"}, {"antigravity", "请在浏览器完成 Google 登录"},
		{"devin", "手工粘贴"}, {"kimi", "ABCD-1234"}, {"kimi-ai", "ABCD-1234"}, {"xai", "ABCD-1234"}, {"meta", "ABCD-1234"},
	} {
		t.Run(c.provider, func(t *testing.T) {
			p, ok := reauth.ProviderFor(c.provider)
			if !ok {
				t.Fatal(c.provider)
			}
			j := repository.ReauthJob{Provider: p.ID, Flow: p.Flow, CodeKind: p.CodeKind, AccountLabel: "Synthetic account", UserCode: "ABCD-1234", VerificationURL: "https://verify.example.test/device", ExpiresAtMS: 1792454400000}
			card := OAuthCard(j, "request", "ou_contact")
			body, _ := json.Marshal(card)
			if !strings.Contains(string(body), c.want) || !strings.Contains(string(body), p.Name) {
				t.Fatalf("card=%s", body)
			}
			if p.CodeKind != "otp" && strings.Contains(string(body), "6 位") {
				t.Fatalf("non-OTP card requests email code: %s", body)
			}
			if p.CodeKind == "device" && (!strings.Contains(string(body), j.VerificationURL) || !strings.Contains(string(body), "2026-10-20T00:00:00Z")) {
				t.Fatalf("device instructions missing: %s", body)
			}
		})
	}
}

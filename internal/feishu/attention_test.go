package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestAttentionEpisodeNotifiesOnceThroughExhaustionAndRecovery(t *testing.T) {
	now := mustTime("2026-10-26T12:00:00Z")
	report := Report{GeneratedAt: now, Providers: []ProviderReport{{Provider: "codex", Accounts: []AccountReport{{Key: "synthetic", Label: "work", State: StateHealthy, OK: true, ObservedAt: now, Windows: []Window{accountWindow(90, nil)}}}}}}
	config := PolicyConfig{EnableAttention: true, TightRemainingPercent: 20}
	first := evaluatePolicy(config, PolicyInput{Report: report, Now: now})
	if len(first.Alerts) != 1 || first.Alerts[0].Kind != AlertQuotaLow {
		t.Fatalf("first episode: %+v", first)
	}
	second := evaluatePolicy(config, PolicyInput{Report: report, Prev: first.Items, Now: now})
	if len(second.Alerts) != 0 {
		t.Fatal("unchanged episode alerted again")
	}
	report.Providers[0].Accounts[0].Windows = []Window{accountWindow(100, nil)}
	exhausted := evaluatePolicy(config, PolicyInput{Report: report, Prev: second.Items, Now: now})
	if len(exhausted.Alerts) != 0 {
		t.Fatalf("escalation: %+v", exhausted)
	}
	report.Providers[0].Accounts[0].Windows = []Window{accountWindow(20, nil)}
	recovered := evaluatePolicy(config, PolicyInput{Report: report, Prev: exhausted.Items, Now: now})
	if len(openItems(recovered.Items)) != 0 || len(recovered.Alerts) != 0 {
		t.Fatalf("recovery: %+v", recovered)
	}
	report.Providers[0].Accounts[0].Windows = []Window{accountWindow(90, nil)}
	if recurring := evaluatePolicy(config, PolicyInput{Report: report, Prev: recovered.Items, Now: now}); len(recurring.Alerts) != 0 {
		t.Fatalf("recurrence: %+v", recurring)
	}
}

func TestDecisionCardBoundsAndMissingData(t *testing.T) {
	now := mustTime("2026-10-26T12:00:00Z")
	renderer := NewPlainReportRenderer(time.UTC)
	renderer.consoleURL = ConsoleAddress("https://console.example.test", "/omc")
	alerts := []Alert{}
	for i := 0; i < 8; i++ {
		alerts = append(alerts, Alert{Kind: AlertReadingMissing, Provider: "codex", Key: fmt.Sprintf("synthetic-%d", i), Alias: "<at id=all>work</at>", Title: "数据缺失", Advice: "检查采集链路"})
	}
	for _, card := range []map[string]any{renderer.Card(Message{Alerts: alerts, GeneratedAt: now}), renderer.CardSimple(Message{Alerts: alerts, GeneratedAt: now})} {
		elements := card["body"].(map[string]any)["elements"].([]map[string]any)
		if len(elements) != 6 {
			t.Fatalf("elements=%d, want conclusion + 3 points + metadata + button", len(elements))
		}
		encoded, _ := json.Marshal(card)
		payload := string(encoded)
		if strings.Contains(payload, "0%") || strings.Contains(payload, "<at") || strings.Contains(payload, "callback") {
			t.Fatalf("unsafe or false content: %s", payload)
		}
		if strings.Count(payload, "建议：") != 3 || !strings.Contains(payload, "另有 5 项") {
			t.Fatalf("bounded points missing: %s", payload)
		}
		button := elements[len(elements)-1]
		if button["behaviors"].([]map[string]any)[0]["default_url"] != "https://console.example.test/omc" {
			t.Fatalf("button=%+v", button)
		}
	}
}

func TestMorningFallbackAndAfternoonSilence(t *testing.T) {
	env := newTestEnv(t, Report{}, false)
	*env.clock = mustTime("2026-10-26T10:00:00Z")
	env.service.tick(context.Background())
	env.service.tick(context.Background())
	if sends, _ := env.sender.counts(); sends != 1 {
		t.Fatalf("morning sends=%d", sends)
	}
	if payload := marshalTestJSON(env.sender.lastCard()); !strings.Contains(payload, "数据缺失") || strings.Contains(payload, "0%") {
		t.Fatal(payload)
	}
	*env.clock = mustTime("2026-10-26T17:00:00Z")
	env.service.tick(context.Background())
	if sends, _ := env.sender.counts(); sends != 1 {
		t.Fatalf("afternoon sends=%d", sends)
	}
	*env.clock = mustTime("2026-10-27T10:00:00Z")
	env.service.tick(context.Background())
	if sends, _ := env.sender.counts(); sends != 2 {
		t.Fatalf("next morning sends=%d", sends)
	}
}

func TestNotificationRecipientIsOnePerson(t *testing.T) {
	web, overlay := readyConfig()
	web.ChatID = "oc_group"
	if resolveConfig(web, overlay).Ready() {
		t.Fatal("group must not receive quota notifications")
	}
	web.ChatID = "ou_synthetic"
	if !resolveConfig(web, overlay).Ready() || receiveIDType(web.ChatID) != "open_id" {
		t.Fatal("personal recipient must use open_id")
	}
	if receiveIDType("oc_oauth") != "chat_id" {
		t.Fatal("OAuth group transport changed")
	}
	env := newTestEnv(t, Report{}, false)
	_ = env.service.HandleMessage(context.Background(), Inbound{ChatID: "oc_private", ChatType: "p2p", SenderOpenID: "ou_other", Text: "额度"})
	if env.source.readCount() != 0 {
		t.Fatal("another person read quota data")
	}
}

func TestPredictionUsesExhaustionClockAndSurplusIsQuiet(t *testing.T) {
	now := mustTime("2026-10-26T17:00:00Z")
	eta := now.Add(90 * time.Minute).UnixMilli()
	item := PredictionDigestItem{Provider: "codex", CredentialLabel: "work", Verdict: "will_exhaust", ExhaustionETAMS: &eta}
	notification := predictionNotification([]PredictionDigestItem{item}, 0, readyPredictionConfig(), now)
	payload := marshalTestJSON(NewPlainReportRenderer(time.UTC).Card(Message{Alerts: notification.Alerts, Kind: notification.Kind}))
	if !strings.Contains(payload, "预计还可用 1 小时 30 分钟") || !strings.Contains(payload, "当前已用 数据缺失") {
		t.Fatal(payload)
	}
	item.Verdict = "under_used"
	job := &PredictionJob{service: NewService(Options{})}
	if job.queueFindings(EmptyState(), readyPredictionConfig(), now, PredictionDigest{Items: []PredictionDigestItem{item}}) {
		t.Fatal("surplus sent outside morning")
	}
}

func TestThresholdIsInclusiveAndFixedAtTenPercent(t *testing.T) {
	now := mustTime("2026-10-26T12:00:00Z")
	web, overlay := readyConfig()
	web.TightRemainingPercent = 20
	resolved := resolveConfig(web, overlay)
	if resolved.TightRemainingPercent != 10 {
		t.Fatal("stored legacy threshold changed the agreed trigger")
	}
	for _, testCase := range []struct {
		used  float64
		count int
	}{{89.9, 0}, {90, 1}, {100, 1}} {
		report := Report{Providers: []ProviderReport{{Provider: "codex", Accounts: []AccountReport{{State: StateHealthy, OK: true, ObservedAt: now, Windows: []Window{accountWindow(testCase.used, nil)}}}}}}
		if alerts := attentionSignals(report, resolved.TightRemainingPercent, now); len(alerts) != testCase.count {
			t.Fatalf("used=%v alerts=%+v", testCase.used, alerts)
		}
	}
}

func TestDecisionMergesForecastWithReadingAndKeepsStaleAuthUnknown(t *testing.T) {
	now := mustTime("2026-10-26T12:00:00Z")
	forecast := Alert{Kind: predictionKindPrefix, Provider: "codex", Key: "work", Scope: "7d", Title: "预计重置前耗尽"}
	reading := Alert{Kind: AlertQuotaLow, Provider: "codex", Key: "work", Scope: "7d", Title: "额度将耗尽"}
	if alerts := decisionAlerts(Message{Alerts: []Alert{forecast, reading}}); len(alerts) != 1 || alerts[0].Kind != AlertQuotaLow {
		t.Fatalf("same risk displayed twice: %+v", alerts)
	}
	report := Report{Providers: []ProviderReport{{Provider: "codex", Accounts: []AccountReport{{Key: "work", AuthFailed: true, State: StateStale, ObservedAt: now}}}}}
	alerts := reportDecisionAlerts(report, true, now)
	if len(alerts) != 1 || alerts[0].Kind != AlertReadingMissing {
		t.Fatalf("stale authentication became healthy: %+v", alerts)
	}
}

func TestPredictionEpisodePersistsAndSharesLowQuotaIdentity(t *testing.T) {
	now := mustTime("2026-10-26T12:00:00Z")
	reset := now.Add(12 * time.Hour).UnixMilli()
	item := PredictionDigestItem{Key: "synthetic", Provider: "codex", CredentialLabel: "work", WindowID: "7d", WindowKind: "weekly", ResetAtMS: &reset, Verdict: "will_exhaust"}
	job := &PredictionJob{service: NewService(Options{})}
	state := EmptyState()
	if !job.queueFindings(state, readyPredictionConfig(), now, PredictionDigest{Items: []PredictionDigestItem{item}}) {
		t.Fatal("event outside schedule was lost")
	}
	store := NewMemoryStore()
	if err := store.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	reloaded, _ := store.Load(context.Background())
	if job.queueFindings(reloaded, readyPredictionConfig(), now.Add(time.Minute), PredictionDigest{Items: []PredictionDigestItem{item}}) {
		t.Fatal("persisted event repeated")
	}
	morning := mustTime("2026-10-27T10:00:00Z")
	if !job.queueFindings(reloaded, readyPredictionConfig(), morning, PredictionDigest{Items: []PredictionDigestItem{item}}) {
		t.Fatal("morning recap lost an ongoing prediction")
	}
	resetTime := time.UnixMilli(reset)
	report := Report{Providers: []ProviderReport{{Provider: "codex", Accounts: []AccountReport{{Key: "synthetic", State: StateHealthy, OK: true, ObservedAt: now, Windows: []Window{accountWindow(95, &resetTime)}}}}}}
	result := evaluatePolicy(PolicyConfig{EnableAttention: true, TightRemainingPercent: 10}, PolicyInput{Report: report, Prev: reloaded.PendingItems, Now: now})
	if len(result.Alerts) != 0 {
		t.Fatalf("low quota repeated prediction: %+v", result.Alerts)
	}
	nextReset := reset + int64((7*24*time.Hour)/time.Millisecond)
	item.ResetAtMS = &nextReset
	if !job.queueFindings(reloaded, readyPredictionConfig(), now.Add(time.Minute), PredictionDigest{Items: []PredictionDigestItem{item}}) {
		t.Fatal("new reset cycle was suppressed")
	}
}

func TestMorningPredictionFirstDoesNotRepeatLowQuota(t *testing.T) {
	now := mustTime("2026-10-26T10:00:00Z")
	env := newTestEnv(t, Report{GeneratedAt: now, Providers: []ProviderReport{{Provider: "claude", Accounts: []AccountReport{{Key: "low", State: StateHealthy, OK: true, ObservedAt: now, Windows: []Window{accountWindow(95, nil)}}}}}}, true)
	*env.clock = now
	job := NewPredictionJob(env.service, func(context.Context) (PredictionDigest, error) {
		return PredictionDigest{Items: []PredictionDigestItem{{Key: "forecast", Provider: "codex", Verdict: "will_exhaust"}}}, nil
	})
	job.Activate()
	job.tick(context.Background())
	env.service.tick(context.Background())
	if sends, _ := env.sender.counts(); sends != 1 {
		t.Fatalf("same morning risk sent %d cards, want 1", sends)
	}
}

func TestMorningFallbackFirstDoesNotRepeatLowQuota(t *testing.T) {
	now := mustTime("2026-10-26T10:00:00Z")
	env := newTestEnv(t, Report{GeneratedAt: now, Providers: []ProviderReport{{Provider: "claude", Accounts: []AccountReport{{Key: "low", State: StateHealthy, OK: true, ObservedAt: now, Windows: []Window{accountWindow(95, nil)}}}}}}, true)
	*env.clock = now
	job := NewPredictionJob(env.service, func(context.Context) (PredictionDigest, error) { return PredictionDigest{}, nil })
	job.Activate()
	job.tick(context.Background())
	env.service.tick(context.Background())
	if sends, _ := env.sender.counts(); sends != 1 {
		t.Fatalf("same fallback morning risk sent %d cards", sends)
	}
}

func TestMissingReportMorningDoesNotLeaveGlobalIncident(t *testing.T) {
	now := mustTime("2026-10-26T10:00:00Z")
	env := newTestEnv(t, Report{}, false)
	*env.clock = now
	job := NewPredictionJob(env.service, func(context.Context) (PredictionDigest, error) {
		return PredictionDigest{Items: []PredictionDigestItem{{Key: "forecast", Provider: "codex", Verdict: "will_exhaust"}}}, nil
	})
	job.tick(context.Background())
	state, _ := env.store.Load(context.Background())
	for _, item := range state.PendingItems {
		if item.Provider == "" && item.Status == PendingOpen {
			t.Fatalf("global missing-data item cannot recover: %+v", item)
		}
	}
}

func TestReadingFailureDoesNotRepeatAsMonitorDown(t *testing.T) {
	now := mustTime("2026-10-26T12:00:00Z")
	report := Report{GeneratedAt: now, Providers: []ProviderReport{{Provider: "codex", Accounts: []AccountReport{{Key: "failure", State: StateStale, ObservedAt: now}}}}}
	config := PolicyConfig{EnableAttention: true, MonitorDownAfter: 30 * time.Minute}
	first := evaluatePolicy(config, PolicyInput{Report: report, Now: now})
	if len(first.Alerts) != 1 || first.Alerts[0].Kind != AlertReadingMissing {
		t.Fatalf("initial failure=%+v", first)
	}
	later := evaluatePolicy(config, PolicyInput{Report: report, Prev: first.Items, Now: now.Add(31 * time.Minute), Tracks: map[string]ChannelTrack{"codex": {FirstSeen: now}}})
	if len(later.Alerts) != 0 {
		t.Fatalf("same failure announced twice: %+v", later.Alerts)
	}
}

func TestManualStatusFailurePersistsAndRetries(t *testing.T) {
	env := newTestEnv(t, Report{}, false)
	env.sender.failSends = 2
	if err := env.service.SendTest(context.Background()); err == nil {
		t.Fatal("failed manual send reported success")
	}
	state, err := env.store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Outbox) != 1 || state.Outbox[0].State != NotifyPending || state.Outbox[0].Attempts != 1 {
		t.Fatalf("lost failed manual card: %+v", state)
	}
	*env.clock = state.Outbox[0].NextAttemptAt.Add(time.Second)
	env.service.tick(context.Background())
	if sends, _ := env.sender.counts(); sends != 1 {
		t.Fatalf("manual retry sends=%d", sends)
	}
}

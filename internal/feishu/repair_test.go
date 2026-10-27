package feishu

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestMovingResetTicksNotifyOnceAndAbsoluteRollover(t *testing.T) {
	for _, provider := range []string{"codex", "ollama"} {
		for _, accuracy := range []string{"derived", "exact"} {
			t.Run(provider+"/"+accuracy, func(t *testing.T) {
				now := mustTime("2026-10-26T12:00:00Z")
				env := newTestEnv(t, Report{}, true)
				boundary := now.Add(time.Hour)
				for i := 0; i < 5; i++ {
					*env.clock = now.Add(time.Duration(i) * time.Minute)
					reset := boundary.Add(time.Duration(i) * time.Minute)
					w := accountWindow(93, &reset)
					w.ID = "7d"
					w.Kind = "weekly"
					w.ResetAccuracy = accuracy
					env.source.report = Report{GeneratedAt: *env.clock, Providers: []ProviderReport{{Provider: provider, Accounts: []AccountReport{{Key: "synthetic", State: StateHealthy, OK: true, ObservedAt: *env.clock, Windows: []Window{w}}}}}}
					env.service.tick(context.Background())
				}
				if sends, _ := env.sender.counts(); sends != 1 {
					t.Fatalf("5 ticks sent %d cards", sends)
				}
				if accuracy == "exact" {
					*env.clock = boundary.Add(time.Minute)
					a := &env.source.report.Providers[0].Accounts[0]
					reset := boundary.Add(24 * time.Hour)
					a.Windows[0].ResetAt = &reset
					a.ObservedAt = boundary.Add(-time.Minute)
					env.service.tick(context.Background())
					if sends, _ := env.sender.counts(); sends != 1 {
						t.Fatalf("cached pre-boundary sample sent %d cards", sends)
					}
					a.ObservedAt = *env.clock
					env.service.tick(context.Background())
					if sends, _ := env.sender.counts(); sends != 2 {
						t.Fatalf("actual rollover sent %d cards", sends)
					}
				}
			})
		}
	}
}

func TestPredictionRecoveryRecurrenceAndPruning(t *testing.T) {
	for _, accuracy := range []string{"", "derived", "exact"} {
		t.Run(accuracy, func(t *testing.T) {
			now := mustTime("2026-10-26T12:00:00Z")
			reset := now.Add(time.Hour).UnixMilli()
			item := PredictionDigestItem{Provider: "codex", Key: "synthetic", WindowID: "7d", WindowKind: "weekly", ResetAccuracy: accuracy, ObservedAtMS: now.UnixMilli(), Verdict: "will_exhaust"}
			if accuracy != "" {
				item.ResetAtMS = &reset
			}
			state := EmptyState()
			job := &PredictionJob{service: NewService(Options{})}
			cfg := readyPredictionConfig()
			if !job.queueFindings(state, cfg, now, PredictionDigest{Items: []PredictionDigestItem{item}}) {
				t.Fatal("first risk lost")
			}
			for i := 1; i <= 5; i++ {
				now = now.Add(time.Minute)
				item.ObservedAtMS = now.UnixMilli()
				shifted := reset + int64(i)*60000
				if item.ResetAtMS != nil {
					item.ResetAtMS = &shifted
				}
				if job.queueFindings(state, cfg, now, PredictionDigest{Items: []PredictionDigestItem{item}}) {
					t.Fatal("unchanged risk repeated")
				}
			}
			item.Verdict = "unknown"
			job.queueFindings(state, cfg, now, PredictionDigest{Items: []PredictionDigestItem{item}})
			if !state.PendingItems[0].ResolvedAt.IsZero() {
				t.Fatal("unknown is not recovery")
			}
			item.Verdict = "on_track"
			job.queueFindings(state, cfg, now, PredictionDigest{Items: []PredictionDigestItem{item}})
			if state.PendingItems[0].ResolvedAt.IsZero() {
				t.Fatal("positive recovery has no finite timestamp")
			}
			item.Verdict = "will_exhaust"
			got := job.queueFindings(state, cfg, now.Add(time.Minute), PredictionDigest{Items: []PredictionDigestItem{item}})
			if got != (accuracy != "exact") {
				t.Fatalf("recurrence queued=%v accuracy=%q", got, accuracy)
			}
			item.Verdict = "on_track"
			job.queueFindings(state, cfg, now.Add(2*time.Minute), PredictionDigest{Items: []PredictionDigestItem{item}})
			state.prune(now.Add(30 * 24 * time.Hour))
			if len(state.PendingItems) != 0 {
				t.Fatal("resolved forecast retained forever")
			}
		})
	}
}

func TestActiveForecastAndLegacyResolvedRetention(t *testing.T) {
	now := mustTime("2026-10-26T12:00:00Z")
	state := EmptyState()
	state.PendingItems = []PendingItem{{ID: "legacy", Status: PendingResolved, LastSeen: now.Add(-30 * 24 * time.Hour)}, {ID: "active", Kind: predictionKindPrefix, Status: PendingResolved, Since: now.Add(-30 * 24 * time.Hour), LastSeen: now}}
	state.prune(now)
	if len(state.PendingItems) != 1 || state.PendingItems[0].ID != "active" {
		t.Fatalf("retention=%+v", state.PendingItems)
	}
}

func TestStartupMorningSameMinuteWithPersistedOpenItem(t *testing.T) {
	env := newTestEnv(t, Report{}, false)
	*env.clock = mustTime("2026-10-26T10:00:00Z")
	state := EmptyState()
	state.PendingItems = []PendingItem{{ID: "synthetic", Kind: AlertCredential, Provider: "codex", Key: "synthetic", Status: PendingOpen, Since: *env.clock}}
	if err := env.store.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	env.service.tick(context.Background())
	env.service.tick(context.Background())
	if sends, _ := env.sender.counts(); sends != 1 {
		t.Fatalf("startup and morning sent %d cards", sends)
	}
}

func TestLegacyGroupRecipientShowsActionableDegradation(t *testing.T) {
	web, overlay := readyConfig()
	web.ChatID = "oc_synthetic"
	cfg := resolveConfig(web, overlay)
	if !cfg.Degraded || cfg.Ready() || !strings.Contains(cfg.Reason, "Open ID") || !strings.Contains(cfg.Reason, "ou_") {
		t.Fatalf("config=%+v", cfg)
	}
}

func TestMorningRecurrencePredictionFirst(t *testing.T) {
	for _, hasForecast := range []bool{false, true} {
		for _, hasReset := range []bool{false, true} {
			t.Run(fmt.Sprintf("forecast=%v/reset=%v", hasForecast, hasReset), func(t *testing.T) {
				now := mustTime("2026-10-26T12:00:00Z")
				morning := mustTime("2026-10-27T10:00:00Z")
				boundary := morning.Add(10 * time.Minute)
				env := newTestEnv(t, Report{}, true)
				setReading := func(at time.Time, used float64, reset time.Time) {
					*env.clock = at
					w := accountWindow(used, nil)
					if hasReset {
						w.ResetAt = &reset
					}
					env.source.report = Report{GeneratedAt: at, Providers: []ProviderReport{{Provider: "codex", Accounts: []AccountReport{{Key: "synthetic", State: StateHealthy, OK: true, ObservedAt: at, Windows: []Window{w}}}}}}
				}
				setReading(now, 95, now.Add(time.Hour))
				env.service.tick(context.Background())
				for sample := 1; sample <= recoveryObservationCount; sample++ {
					setReading(now.Add(time.Duration(sample)*time.Minute), 20, now.Add(time.Hour))
					env.service.tick(context.Background())
				}
				setReading(morning, 95, boundary)
				job := NewPredictionJob(env.service, func(context.Context) (PredictionDigest, error) {
					if hasForecast {
						return PredictionDigest{Items: []PredictionDigestItem{{Provider: "ollama", Key: "forecast", WindowID: "7d", Verdict: "will_exhaust"}}}, nil
					}
					return PredictionDigest{}, nil
				})
				job.Activate()
				job.tick(context.Background())
				env.service.tick(context.Background())
				if sends, _ := env.sender.counts(); sends != 2 {
					t.Fatalf("previous episode and morning recurrence sent %d cards", sends)
				}
				state, err := env.store.Load(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				identity := ItemID(AlertQuotaLow, "codex", "synthetic", "7d")
				item, exists := state.pendingByID(identity)
				if !exists || item.Status != PendingOpen || !item.ResolvedAt.IsZero() || !item.Since.Equal(morning) || !item.NotifiedAt.Equal(morning) || hasReset && !item.CycleResetAt.Equal(boundary) {
					t.Fatalf("morning recurrence not acknowledged: %+v", item)
				}
				*env.clock = morning.Add(30 * time.Second)
				account := env.source.report.Providers[0].Accounts[0]
				account.Key = "later-risk"
				account.ObservedAt = *env.clock
				env.source.report.Providers[0].Accounts = append(env.source.report.Providers[0].Accounts, account)
				env.service.tick(context.Background())
				if sends, _ := env.sender.counts(); sends != 3 {
					t.Fatalf("unseen risk later in the summary minute suppressed: %d", sends)
				}
				if hasReset {
					setReading(morning.Add(time.Minute), 95, boundary.Add(time.Minute))
					env.service.tick(context.Background())
					if sends, _ := env.sender.counts(); sends != 3 {
						t.Fatalf("revised future reset repeated risk: %d", sends)
					}
					setReading(boundary, 95, boundary.Add(24*time.Hour))
					env.service.tick(context.Background())
					if sends, _ := env.sender.counts(); sends != 4 {
						t.Fatalf("observed new cycle suppressed: %d", sends)
					}
				}
			})
		}
	}
}

func TestForecastLearnsLaterExactCycle(t *testing.T) {
	for _, accuracy := range []string{"", "derived"} {
		t.Run(accuracy, func(t *testing.T) {
			now := mustTime("2026-10-26T12:00:00Z")
			boundary := now.Add(time.Hour)
			reset := boundary.UnixMilli()
			item := PredictionDigestItem{Provider: "codex", Key: "synthetic", WindowID: "7d", WindowKind: "weekly", ResetAccuracy: accuracy, ResetAtMS: &reset, ObservedAtMS: now.UnixMilli(), Verdict: "will_exhaust"}
			state := EmptyState()
			job := &PredictionJob{service: NewService(Options{})}
			cfg := readyPredictionConfig()
			if !job.queueFindings(state, cfg, now, PredictionDigest{Items: []PredictionDigestItem{item}}) {
				t.Fatal("first risk absent")
			}
			item.ResetAccuracy = "exact"
			item.ObservedAtMS = now.Add(time.Minute).UnixMilli()
			if job.queueFindings(state, cfg, now.Add(time.Minute), PredictionDigest{Items: []PredictionDigestItem{item}}) {
				t.Fatal("learning boundary repeated current risk")
			}
			if !state.PendingItems[0].CycleResetAt.Equal(boundary) || !state.PendingItems[0].Since.Equal(now) || !state.PendingItems[0].NotifiedAt.Equal(now) {
				t.Fatalf("first exact boundary not learned in existing episode: %+v", state.PendingItems)
			}
			nextReset := boundary.Add(7 * 24 * time.Hour).UnixMilli()
			item.ResetAtMS = &nextReset
			if job.queueFindings(state, cfg, boundary.Add(time.Minute), PredictionDigest{Items: []PredictionDigestItem{item}}) {
				t.Fatal("cached pre-boundary observation opened a cycle")
			}
			item.ObservedAtMS = boundary.Add(time.Minute).UnixMilli()
			if !job.queueFindings(state, cfg, boundary.Add(time.Minute), PredictionDigest{Items: []PredictionDigestItem{item}}) {
				t.Fatalf("observed new exact cycle suppressed: %+v", state.PendingItems)
			}
		})
	}
}

func TestLegacyQuotaRecipientPreservesOAuthTransport(t *testing.T) {
	for _, mode := range []string{"enabled", "forced-off", "missing-app-id", "missing-app-secret"} {
		t.Run(mode, func(t *testing.T) {
			web, overlay := readyConfig()
			web.ChatID, overlay.OAuthChatID = "oc_synthetic", "oc_oauth"
			switch mode {
			case "forced-off":
				overlay.ForceDisabled = true
			case "missing-app-id":
				web.AppID = ""
			case "missing-app-secret":
				overlay.AppSecret = nil
			}
			sender := newFakeSender()
			factory := newCountingSourceFactory()
			bot := NewService(Options{Web: web, Overlay: overlay, SenderFactory: func(Resolved) Sender { return sender }, SourceFactory: factory.New})
			accepted := false
			bot.ConfigureOAuth("oc_oauth", "ou_contact", func(context.Context, string, string) error { accepted = true; return nil })
			wantReady := mode == "enabled"
			cfg := bot.resolvedNow()
			if cfg.Ready() || cfg.ConnectionReady() != wantReady || bot.OAuthReady() != wantReady {
				t.Fatalf("quota/OAuth readiness=%v/%v/%v, want false/%v/%v", cfg.Ready(), cfg.ConnectionReady(), bot.OAuthReady(), wantReady, wantReady)
			}
			if wantReady {
				if !cfg.Degraded || !strings.Contains(cfg.Reason, "Open ID") {
					t.Fatalf("quota degradation missing: %q", cfg.Reason)
				}
				if err := bot.Start(context.Background()); err != nil {
					t.Fatal(err)
				}
				defer bot.Stop()
				waitEvent(t, "OAuth connection", factory.signal, func() bool { starts, _ := factory.counts(); return starts == 1 })
			}
			if err := bot.HandleMessage(context.Background(), Inbound{MessageID: "synthetic-msg", ChatID: "oc_oauth", ChatType: "group", SenderOpenID: "ou_contact", MessageType: "text", Text: "123456"}); err != nil {
				t.Fatal(err)
			}
			if accepted != wantReady {
				t.Fatalf("OAuth code accepted=%v, want %v", accepted, wantReady)
			}
		})
	}
}

type deliveryFailureStore struct {
	Store
	saves    int
	loads    int
	failSave int
	failLoad int
	err      error
}

func (store *deliveryFailureStore) Save(ctx context.Context, state *State) error {
	store.saves++
	if store.saves == store.failSave {
		return store.err
	}
	return store.Store.Save(ctx, state)
}

func (store *deliveryFailureStore) Load(ctx context.Context) (*State, error) {
	store.loads++
	if store.loads == store.failLoad {
		return nil, store.err
	}
	return store.Store.Load(ctx)
}

func TestSendTestPropagatesDeliveryPersistenceFailure(t *testing.T) {
	for _, phase := range []string{"load-outbox", "load-latest", "save-attempt"} {
		t.Run(phase, func(t *testing.T) {
			env := newTestEnv(t, Report{}, true)
			wantErr := errors.New("synthetic delivery persistence failure")
			store := &deliveryFailureStore{Store: env.store, err: wantErr}
			switch phase {
			case "load-outbox":
				store.failLoad = 2
			case "load-latest":
				store.failLoad = 3
			case "save-attempt":
				store.failSave = 2
			}
			env.service.store = store
			env.sender.failSends = 2
			delivery, err := env.service.SendTest(context.Background())
			if !errors.Is(err, wantErr) || delivery.Sent || delivery.QueuedForRetry || delivery.Attempts != 0 || !delivery.NextAttemptAt.IsZero() {
				t.Fatalf("unpersisted attempt reported as delivery=%+v error=%v", delivery, err)
			}
			state, err := store.Load(context.Background())
			if err != nil || len(state.Outbox) != 1 || state.Outbox[0].Attempts != 0 || !state.Outbox[0].NextAttemptAt.IsZero() {
				t.Fatalf("initial insert must survive, state=%+v error=%v", state, err)
			}
		})
	}
}

func TestMorningPredictionPreservesObservedQuotaCycle(t *testing.T) {
	now := mustTime("2026-10-26T10:00:00Z")
	reset := now.Add(time.Hour)
	env := newTestEnv(t, Report{GeneratedAt: now, Providers: []ProviderReport{{Provider: "claude", Accounts: []AccountReport{{Key: "low", State: StateHealthy, OK: true, ObservedAt: now, Windows: []Window{accountWindow(95, &reset)}}}}}}, true)
	*env.clock = now
	job := NewPredictionJob(env.service, func(context.Context) (PredictionDigest, error) {
		return PredictionDigest{Items: []PredictionDigestItem{{Provider: "codex", Key: "forecast", WindowID: "7d", Verdict: "will_exhaust"}}}, nil
	})
	job.Activate()
	job.tick(context.Background())
	env.service.tick(context.Background())
	*env.clock = reset.Add(time.Minute)
	account := &env.source.report.Providers[0].Accounts[0]
	account.ObservedAt = *env.clock
	next := reset.Add(24 * time.Hour)
	account.Windows[0].ResetAt = &next
	env.service.tick(context.Background())
	if sends, _ := env.sender.counts(); sends != 2 {
		t.Fatalf("morning then true rollover sends=%d", sends)
	}
}

func TestQueryHandlerPreservesScopedAnswerAndStaleNotice(t *testing.T) {
	now := mustTime("2026-10-26T12:00:00Z")
	for _, tc := range []struct {
		text, want string
		stale      bool
	}{{"异常", "当前没有异常项。", false}, {"gemini", "未找到该渠道的数据。", false}, {"额度", "以下为", true}} {
		t.Run(tc.text, func(t *testing.T) {
			observed := now
			if tc.stale {
				observed = now.Add(-time.Hour)
			}
			report := Report{GeneratedAt: observed, Providers: []ProviderReport{{Provider: "codex", Accounts: []AccountReport{{Key: "synthetic", State: StateHealthy, OK: true, ObservedAt: observed, Windows: []Window{accountWindow(20, nil)}}}}}}
			env := newTestEnv(t, report, true)
			*env.clock = now
			err := env.service.HandleMessage(context.Background(), Inbound{EventID: fmt.Sprint(tc.text), MessageID: "synthetic-message", ChatID: "oc_synthetic", ChatType: "p2p", SenderOpenID: "ou_test", MessageType: "text", Text: tc.text})
			if err != nil {
				t.Fatal(err)
			}
			env.service.taskGroup().wg.Wait()
			payload := marshalTestJSON(env.sender.lastCard())
			if !strings.Contains(payload, tc.want) {
				t.Fatalf("missing answer %q: %s", tc.want, payload)
			}
			if !tc.stale && strings.Contains(payload, "数据缺失") {
				t.Fatal(payload)
			}
		})
	}
}

func TestMorningBypassesQuietWhileOrdinaryAlertWaits(t *testing.T) {
	env := newTestEnv(t, Report{}, false)
	web, overlay := readyConfig()
	web.SummaryTimes = []string{"07:00"}
	env.service.Reload(context.Background(), web, overlay)
	*env.clock = mustTime("2026-10-26T07:00:00Z")
	env.service.tick(context.Background())
	if sends, _ := env.sender.counts(); sends != 1 {
		t.Fatalf("morning sends=%d", sends)
	}
	*env.clock = env.clock.Add(time.Minute)
	env.source.has = true
	env.source.report = invalidReport(*env.clock)
	env.service.tick(context.Background())
	if sends, _ := env.sender.counts(); sends != 1 {
		t.Fatalf("quiet alert sent: %d", sends)
	}
	*env.clock = mustTime("2026-10-26T08:00:00Z")
	env.service.tick(context.Background())
	if sends, _ := env.sender.counts(); sends != 2 {
		t.Fatalf("quiet end sends=%d", sends)
	}
}

package feishu

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestQuotaThresholdJitterKeepsOneEpisode(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ticks   int
		step    time.Duration
		derived bool
	}{
		{"no-reset", 30, time.Minute, false},
		{"derived-reset", 6, 5 * time.Minute, true},
		{"cached-five-minute-samples", 60, time.Minute, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t, Report{}, true)
			base := mustTime("2026-10-27T12:00:00Z")
			for tick := 0; tick < tc.ticks; tick++ {
				now := base.Add(time.Duration(tick) * tc.step)
				observed := now
				sample := tick
				if tc.name == "cached-five-minute-samples" {
					sample = tick / 5
					observed = base.Add(time.Duration(sample) * 5 * time.Minute)
				}
				used := 91.0
				if sample%2 == 1 {
					used = 89.5
				}
				window := accountWindow(used, nil)
				if tc.derived {
					reset := now.Add(5 * time.Hour)
					window.ResetAt = &reset
					window.ResetAccuracy = "derived"
				}
				*env.clock = now
				env.source.report = Report{GeneratedAt: observed, Providers: []ProviderReport{{Provider: "codex", Accounts: []AccountReport{{Key: "synthetic", State: StateHealthy, OK: true, ObservedAt: observed, Windows: []Window{window}}}}}}
				env.service.tick(context.Background())
			}
			if sends, _ := env.sender.counts(); sends != 1 {
				t.Fatalf("jitter sent %d cards, want 1", sends)
			}
		})
	}
}

func TestQuotaMissingEvidenceDoesNotResolveWindow(t *testing.T) {
	for _, missing := range []string{"window", "account", "provider", "report", "percent", "stale"} {
		t.Run(missing, func(t *testing.T) {
			now := mustTime("2026-10-27T12:00:00Z")
			report := Report{GeneratedAt: now, Providers: []ProviderReport{{Provider: "codex", Accounts: []AccountReport{{Key: "synthetic", State: StateHealthy, OK: true, ObservedAt: now, Windows: []Window{accountWindow(91, nil)}}}}}}
			env := newTestEnv(t, report, true)
			*env.clock = now
			env.service.tick(context.Background())
			identity := ItemID(AlertQuotaLow, "codex", "synthetic", "7d")
			absent := report
			absent.Providers = append([]ProviderReport(nil), report.Providers...)
			absent.Providers[0].Accounts = append([]AccountReport(nil), report.Providers[0].Accounts...)
			switch missing {
			case "window":
				w := accountWindow(20, nil)
				w.Name = "other"
				absent.Providers[0].Accounts[0].Windows = []Window{w}
			case "account":
				absent.Providers[0].Accounts = nil
			case "provider", "report":
				absent.Providers = nil
			case "percent":
				w := accountWindow(20, nil)
				w.UsedPercent = nil
				absent.Providers[0].Accounts[0].Windows = []Window{w}
			case "stale":
				absent.Providers[0].Accounts[0].ObservedAt = now.Add(-time.Hour)
			}
			*env.clock = now.Add(time.Minute)
			env.source.report = absent
			env.source.has = missing != "report"
			env.service.tick(context.Background())
			state, _ := env.store.Load(context.Background())
			item, ok := state.pendingByID(identity)
			if !ok || item.Status != PendingOpen {
				t.Fatalf("missing %s resolved quota: %+v", missing, item)
			}
			*env.clock = now.Add(2 * time.Minute)
			report.Providers[0].Accounts[0].ObservedAt = *env.clock
			env.source.report = report
			env.source.has = true
			env.service.tick(context.Background())
			state, _ = env.store.Load(context.Background())
			count := 0
			for _, n := range state.Outbox {
				for _, a := range n.Alerts {
					if alertItemID(a) == identity {
						count++
					}
				}
			}
			if count != 1 {
				t.Fatalf("returning low window announced %d times", count)
			}
		})
	}
}

func TestQuotaRecoveryRequiresConsecutiveNewObservations(t *testing.T) {
	base := mustTime("2026-10-27T12:00:00Z")
	env := newTestEnv(t, Report{}, true)
	identity := ItemID(AlertQuotaLow, "codex", "synthetic", "7d")
	tick := func(minute int, used float64, observed time.Time) {
		*env.clock = base.Add(time.Duration(minute) * time.Minute)
		env.source.report = Report{GeneratedAt: observed, Providers: []ProviderReport{{Provider: "codex", Accounts: []AccountReport{{Key: "synthetic", State: StateHealthy, OK: true, ObservedAt: observed, Windows: []Window{accountWindow(used, nil)}}}}}}
		env.service.tick(context.Background())
	}
	checkOpen := func(want bool) {
		t.Helper()
		state, _ := env.store.Load(context.Background())
		item, _ := state.pendingByID(identity)
		if (item.Status == PendingOpen) != want {
			t.Fatalf("item=%+v", item)
		}
	}
	tick(0, 91, base)
	tick(1, 20, base.Add(time.Minute))
	checkOpen(true)
	tick(2, 20, base.Add(time.Minute))
	tick(3, 20, base.Add(time.Minute))
	checkOpen(true)
	// Missing evidence breaks the sequence even when a later sample is healthy.
	env.source.report = Report{}
	env.source.has = false
	*env.clock = base.Add(4 * time.Minute)
	env.service.tick(context.Background())
	checkOpen(true)
	env.source.has = true
	for minute := 5; minute < 8; minute++ {
		tick(minute, 20, base.Add(time.Duration(minute)*time.Minute))
		checkOpen(minute < 7)
	}
	tick(8, 91, base.Add(8*time.Minute))
	checkOpen(true)
	if sends, _ := env.sender.counts(); sends != 2 {
		t.Fatalf("stable recovery then recurrence sends=%d, want 2", sends)
	}
}

func TestStartupEpisodesAreNotRepeatedInMorning(t *testing.T) {
	for _, forecast := range []bool{false, true} {
		for _, newRisk := range []bool{false, true} {
			t.Run(fmt.Sprintf("forecast=%v/new=%v", forecast, newRisk), func(t *testing.T) {
				startup := mustTime("2026-10-27T09:50:00Z")
				env := newTestEnv(t, Report{}, false)
				*env.clock = startup
				old := PendingItem{ID: ItemID(AlertCredential, "codex", "old", ""), Kind: AlertCredential, Provider: "codex", Key: "old", Alias: "already-reported", Status: PendingOpen, Since: startup.Add(-time.Hour)}
				state := EmptyState()
				state.PendingItems = []PendingItem{old}
				if err := env.store.Save(context.Background(), state); err != nil {
					t.Fatal(err)
				}
				env.service.tick(context.Background())
				if sends, _ := env.sender.counts(); sends != 1 {
					t.Fatalf("startup sends=%d", sends)
				}
				// Only persisted state may carry the startup acknowledgement across a
				// service replacement; no in-memory minute flag can satisfy this case.
				web, overlay := readyConfig()
				env.service = NewService(Options{Web: web, Overlay: overlay, Store: env.store, Source: env.source, Now: func() time.Time { return *env.clock }, SenderFactory: func(Resolved) Sender { return env.sender }, SourceFactory: func(Resolved) EventSource { return env.events }})
				if !strings.Contains(marshalTestJSON(env.sender.lastCard()), "already-reported") {
					t.Fatal("startup omitted persisted item")
				}
				*env.clock = mustTime("2026-10-27T10:00:00Z")
				if newRisk {
					state, _ = env.store.Load(context.Background())
					fresh := old
					fresh.ID = ItemID(AlertCredential, "codex", "new", "")
					fresh.Key = "new"
					fresh.Alias = "new-risk"
					fresh.Since = *env.clock
					state.PendingItems = append(state.PendingItems, fresh)
					if err := env.store.Save(context.Background(), state); err != nil {
						t.Fatal(err)
					}
					env.source.has = true
					env.source.report = Report{GeneratedAt: *env.clock, Providers: []ProviderReport{{Provider: "codex", Accounts: []AccountReport{{Key: "old", Label: "already-reported", State: StateInvalid, ObservedAt: *env.clock}, {Key: "new", Label: "new-risk", State: StateInvalid, ObservedAt: *env.clock}}}}}
				}
				job := NewPredictionJob(env.service, func(context.Context) (PredictionDigest, error) {
					if forecast {
						return PredictionDigest{Items: []PredictionDigestItem{{Provider: "ollama", Key: "forecast", WindowID: "weekly", Verdict: "will_exhaust"}}}, nil
					}
					return PredictionDigest{}, nil
				})
				job.Activate()
				job.tick(context.Background())
				env.service.tick(context.Background())
				if sends, _ := env.sender.counts(); sends != 2 {
					t.Fatalf("startup plus daily morning sends=%d, want 2", sends)
				}
				payload := marshalTestJSON(env.sender.lastCard())
				if strings.Contains(payload, "already-reported") {
					t.Fatalf("morning repeated startup item: %s", payload)
				}
				if newRisk && !strings.Contains(payload, "new-risk") {
					t.Fatalf("morning omitted later item: %s", payload)
				}
			})
		}
	}
}

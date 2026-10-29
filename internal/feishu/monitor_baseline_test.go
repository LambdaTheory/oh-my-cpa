package feishu

import (
	"context"
	"testing"
	"time"
)

func monitorReport(providers ...string) Report {
	report := Report{}
	for _, provider := range providers {
		report.Providers = append(report.Providers, ProviderReport{
			Provider: provider,
			Accounts: []AccountReport{{Key: provider + "-fixture", State: StateUnknown}},
		})
	}
	return report
}

func restartMonitorService(env *testEnv) {
	web, overlay := readyConfig()
	env.service = NewService(Options{
		Web: web, Overlay: overlay, Store: env.store, Source: env.source,
		Now:           func() time.Time { return *env.clock },
		SenderFactory: func(Resolved) Sender { return env.sender },
		SourceFactory: func(Resolved) EventSource { return env.events },
	})
}

func assertMonitorSends(t *testing.T, env *testEnv, want int) *State {
	t.Helper()
	env.service.tick(context.Background())
	if sends, _ := env.sender.counts(); sends != want {
		t.Fatalf("sent %d cards, want %d", sends, want)
	}
	state, err := env.store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func TestMonitorBaselineStartsAtFirstApplicableObservation(t *testing.T) {
	for _, unknown := range []time.Time{{}, time.UnixMilli(0).UTC(), time.UnixMilli(-1).UTC()} {
		t.Run(unknown.String(), func(t *testing.T) {
			env := newTestEnv(t, monitorReport("codex"), true)
			start := *env.clock
			state := EmptyState()
			state.ChannelTracks["codex"] = ChannelTrack{FirstSeen: unknown, LastValid: unknown}
			if err := env.store.Save(context.Background(), state); err != nil {
				t.Fatal(err)
			}
			state = assertMonitorSends(t, env, 0)
			if !state.ChannelTracks["codex"].FirstSeen.Equal(start) {
				t.Fatalf("unknown baseline was not initialized: %+v", state.ChannelTracks)
			}
			*env.clock = start.Add(30 * time.Minute)
			assertMonitorSends(t, env, 0)
			for sample := 1; sample <= 3; sample++ {
				env.source.report.Providers[0].Accounts[0].AttemptedAt = start.Add(time.Duration(sample) * 10 * time.Minute)
				env.source.report.Providers[0].Accounts[0].CollectionFailed = true
				if sample < 3 {
					assertMonitorSends(t, env, 0)
				}
			}
			restartMonitorService(env)
			*env.clock = start.Add(30*time.Minute + time.Second)
			state = assertMonitorSends(t, env, 1)
			if len(openItems(state.PendingItems)) != 1 || !hasItem(state.PendingItems, AlertMonitorDown) {
				t.Fatalf("actual threshold crossing must open monitor_down: %+v", state.PendingItems)
			}
			restartMonitorService(env)
			*env.clock = env.clock.Add(time.Minute)
			assertMonitorSends(t, env, 1)
		})
	}
}

func TestMonitorPersistedOutageIsNotGivenRestartGrace(t *testing.T) {
	for _, hasSuccess := range []bool{false, true} {
		t.Run(map[bool]string{false: "never-success", true: "last-success"}[hasSuccess], func(t *testing.T) {
			env := newTestEnv(t, monitorReport("codex", "claude"), true)
			state := EmptyState()
			for _, provider := range []string{"codex", "claude"} {
				track := ChannelTrack{FirstSeen: env.clock.Add(-2 * time.Hour), ConsecutiveFailures: 3}
				if hasSuccess {
					track.LastValid = env.clock.Add(-time.Hour)
				}
				state.ChannelTracks[provider] = track
			}
			if err := env.store.Save(context.Background(), state); err != nil {
				t.Fatal(err)
			}
			state = assertMonitorSends(t, env, 1)
			batch, found := notificationOfKind(state.Outbox, KindAlertBatch)
			if !found || len(batch.Alerts) != 2 || len(batch.ItemIDs) != 2 || batch.State != NotifySent {
				t.Fatalf("same-cycle outages must arrive in one acknowledged batch: %+v", state.Outbox)
			}
			if len(openItems(state.PendingItems)) != 2 {
				t.Fatalf("batch must retain separate episode identities: %+v", state.PendingItems)
			}
			restartMonitorService(env)
			assertMonitorSends(t, env, 1)
		})
	}
}

func TestMonitorCachedSuccessAgesFromStoredInstant(t *testing.T) {
	env := newTestEnv(t, monitorReport("codex"), true)
	observed := env.clock.Add(-20 * time.Minute)
	account := &env.source.report.Providers[0].Accounts[0]
	account.State, account.OK = StateHealthy, true
	account.Windows = []Window{accountWindow(25, nil)}
	account.LastSuccessAt, account.ObservedAt = observed, observed
	account.CollectionFailed = true
	account.AttemptedAt = *env.clock
	state := assertMonitorSends(t, env, 0)
	if !state.ChannelTracks["codex"].LastValid.Equal(observed) {
		t.Fatalf("cached reading was restamped: %+v", state.ChannelTracks)
	}
	*env.clock = observed.Add(30 * time.Minute)
	account.AttemptedAt = *env.clock
	assertMonitorSends(t, env, 0)
	restartMonitorService(env)
	*env.clock = env.clock.Add(time.Second)
	account.AttemptedAt = *env.clock
	assertMonitorSends(t, env, 1)

	account.LastSuccessAt, account.ObservedAt = *env.clock, *env.clock
	account.CollectionFailed = false
	account.AttemptedAt = env.clock.Add(time.Second)
	state = assertMonitorSends(t, env, 1)
	if len(openItems(state.PendingItems)) != 0 {
		t.Fatalf("a genuinely new success must resolve without pushing: %+v", state.PendingItems)
	}
}

func TestMonitorIgnoresDisabledAndAuthOnlyEvidence(t *testing.T) {
	env := newTestEnv(t, monitorReport("codex", "claude"), true)
	env.source.report.Providers[0].Accounts[0].Disabled = true
	env.source.report.Providers[1].Accounts[0].State = StateStale
	env.source.report.Providers[1].Accounts[0].AuthFailed = true
	state := EmptyState()
	state.Bootstrapped = true
	for _, provider := range []string{"codex", "claude"} {
		state.ChannelTracks[provider] = ChannelTrack{FirstSeen: env.clock.Add(-time.Hour), ConsecutiveFailures: 3}
	}
	if err := env.store.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	assertMonitorSends(t, env, 0)
}

func TestMonitorBatchQuietRestartAndRetry(t *testing.T) {
	env := newTestEnv(t, monitorReport("codex", "claude"), true)
	*env.clock = time.Date(2026, 10, 15, 23, 30, 0, 0, time.UTC)
	state := EmptyState()
	state.Bootstrapped = true
	for _, provider := range []string{"codex", "claude"} {
		state.ChannelTracks[provider] = ChannelTrack{FirstSeen: env.clock.Add(-time.Hour), ConsecutiveFailures: 3}
	}
	if err := env.store.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	state = assertMonitorSends(t, env, 0)
	if countState(state.Outbox, NotifyQueued) != 2 {
		t.Fatalf("quiet hours must retain individually reconcilable episodes: %+v", state.Outbox)
	}
	restartMonitorService(env)
	*env.clock = time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	env.sender.failSends = 2
	state = assertMonitorSends(t, env, 0)
	batch, found := notificationOfKind(state.Outbox, KindAlertBatch)
	if !found || batch.Attempts != 1 || batch.State != NotifyPending {
		t.Fatalf("failed batch must persist for retry: %+v", state.Outbox)
	}
	restartMonitorService(env)
	*env.clock = batch.NextAttemptAt.Add(time.Second)
	assertMonitorSends(t, env, 1)
	restartMonitorService(env)
	assertMonitorSends(t, env, 1)
}

func TestMonitorExcludesUnsupportedAndResetsPausedBaseline(t *testing.T) {
	env := newTestEnv(t, monitorReport("unsupported", "codex"), true)
	env.source.report.Providers[0].Accounts[0].IsUnmonitored = true
	env.source.report.Providers[1].Accounts[0].Disabled = true
	state := EmptyState()
	state.Bootstrapped = true
	for _, provider := range []string{"unsupported", "codex"} {
		state.ChannelTracks[provider] = ChannelTrack{FirstSeen: env.clock.Add(-time.Hour)}
	}
	if err := env.store.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	state = assertMonitorSends(t, env, 0)
	if len(state.ChannelTracks) != 0 {
		t.Fatalf("excluded sources must not age their monitoring baseline: %+v", state.ChannelTracks)
	}
	*env.clock = env.clock.Add(time.Hour)
	env.source.report.Providers[1].Accounts[0].Disabled = false
	state = assertMonitorSends(t, env, 0)
	if !state.ChannelTracks["codex"].FirstSeen.Equal(*env.clock) {
		t.Fatalf("resuming an unread source needs a new baseline: %+v", state.ChannelTracks)
	}
}

func TestMonitorQuietEpisodeIsDroppedWhenMonitoringStops(t *testing.T) {
	for _, isUnmonitored := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "unsupported"}[isUnmonitored], func(t *testing.T) {
			env := newTestEnv(t, monitorReport("codex"), true)
			*env.clock = time.Date(2026, 10, 15, 23, 30, 0, 0, time.UTC)
			state := EmptyState()
			state.Bootstrapped = true
			state.ChannelTracks["codex"] = ChannelTrack{FirstSeen: env.clock.Add(-time.Hour)}
			if err := env.store.Save(context.Background(), state); err != nil {
				t.Fatal(err)
			}
			assertMonitorSends(t, env, 0)
			account := &env.source.report.Providers[0].Accounts[0]
			account.Disabled, account.IsUnmonitored = !isUnmonitored, isUnmonitored
			*env.clock = time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
			state = assertMonitorSends(t, env, 0)
			if len(openItems(state.PendingItems)) != 0 || countState(state.Outbox, NotifyQueued) != 0 {
				t.Fatalf("an intentional stop must retire a held monitor episode: %+v", state)
			}
		})
	}
}

func TestMonitorUnsupportedQuotaStillAnnouncesCredentialFailure(t *testing.T) {
	env := newTestEnv(t, monitorReport("unsupported"), true)
	account := &env.source.report.Providers[0].Accounts[0]
	account.IsUnmonitored, account.AuthFailed, account.State = true, true, StateInvalid
	state := assertMonitorSends(t, env, 1)
	if !hasItem(state.PendingItems, AlertCredential) || hasItem(state.PendingItems, AlertMonitorDown) {
		t.Fatalf("quota eligibility must not hide independent auth evidence: %+v", state.PendingItems)
	}
}

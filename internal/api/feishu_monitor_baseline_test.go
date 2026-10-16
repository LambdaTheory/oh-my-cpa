package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/feishu"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

func TestFeishuRuntimeUnknownTimestampRoundTrip(t *testing.T) {
	_, repo, _, _ := newFeishuAPITest(t)
	for _, value := range []int64{0, -1} {
		runtime := repository.FeishuRuntime{
			Tracks: []repository.FeishuChannelTrack{{Provider: "codex", FirstSeenMS: &value, LastValidMS: &value}},
		}
		if err := repo.SaveFeishuRuntime(context.Background(), runtime); err != nil {
			t.Fatal(err)
		}
		store := feishuStore{repo: repo}
		state, err := store.Load(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		track := state.ChannelTracks["codex"]
		if !track.FirstSeen.IsZero() || !track.LastValid.IsZero() {
			t.Fatalf("nonpositive timestamps must be absent, not an established outage: %+v", track)
		}
		if err := store.Save(context.Background(), state); err != nil {
			t.Fatal(err)
		}
		saved, err := repo.LoadFeishuRuntime(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(saved.Tracks) != 1 || saved.Tracks[0].FirstSeenMS != nil || saved.Tracks[0].LastValidMS != nil {
			t.Fatalf("unknown instants must round-trip as NULL: %+v", saved.Tracks)
		}
	}
}

func TestFeishuStoredMonitoringEligibilityAndSuccessTime(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name          string
		provider      string
		windows       string
		isUnmonitored bool
		hasSuccess    bool
	}{
		{name: "supported never read", provider: "codex"},
		{name: "unsupported never read", provider: "gemini-cli", isUnmonitored: true},
		{name: "unknown never read", provider: "fixture-plugin", isUnmonitored: true},
		{name: "numeric plugin evidence", provider: "fixture-plugin", windows: `[{"scope":"standard","used_percent":25}]`, hasSuccess: true},
		{name: "no numeric window", provider: "codex", windows: `[{"scope":"standard"}]`},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot := repository.QuotaSnapshotRecord{}
			if test.windows != "" {
				snapshot = repository.QuotaSnapshotRecord{
					AuthIndex: "fixture", Provider: test.provider, Status: "healthy",
					ObservedAtMS: now.Add(-time.Hour).UnixMilli(), WindowsJSON: test.windows,
					PlanJSON: `{"private_payload":"must-not-reach-report"}`,
				}
			}
			account := feishuAccountReport(repository.FeishuCredentialMetadata{
				AuthIndex: "fixture", Driver: test.provider, ObservedAt: now.Add(-24 * time.Hour),
			}, snapshot, repository.FeishuAccountSignal{}, now)
			if account.IsUnmonitored != test.isUnmonitored {
				t.Fatalf("monitoring eligibility = %+v", account)
			}
			if test.hasSuccess {
				if !account.LastSuccessAt.Equal(now.Add(-time.Hour)) {
					t.Fatalf("success must retain its real observation instant: %+v", account)
				}
			} else if !account.LastSuccessAt.IsZero() {
				t.Fatalf("absent numeric evidence must not invent a success: %+v", account)
			}
			encoded, err := json.Marshal(account)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(encoded, []byte("must-not-reach-report")) {
				t.Fatal("private snapshot data reached the report")
			}
			// An unsupported reader does not invalidate independent auth evidence.
			invalid := feishuAccountReport(repository.FeishuCredentialMetadata{
				AuthIndex: "fixture", Driver: test.provider,
			}, snapshot, repository.FeishuAccountSignal{
				State: repository.FeishuSignalAuthInvalid, ObservedAt: now,
			}, now)
			if invalid.State != feishu.StateInvalid || !invalid.AuthFailed {
				t.Fatalf("auth evidence must be independent of quota eligibility: %+v", invalid)
			}
		})
	}
}

type monitorSender struct{ sends atomic.Int64 }

func (sender *monitorSender) SendCard(context.Context, string, map[string]any) error {
	sender.sends.Add(1)
	return nil
}
func (*monitorSender) ReplyCard(context.Context, string, map[string]any) error { return nil }
func (*monitorSender) ReplyText(context.Context, string, string) error         { return nil }

type monitorEvents struct{}

func (monitorEvents) Name() string { return "synthetic-monitor-events" }
func (monitorEvents) Run(ctx context.Context, _ feishu.EventHandler) error {
	<-ctx.Done()
	return nil
}

type monitorStore struct {
	feishuStore
	saves chan struct{}
}

func (store monitorStore) Save(ctx context.Context, state *feishu.State) error {
	if err := store.feishuStore.Save(ctx, state); err != nil {
		return err
	}
	store.saves <- struct{}{}
	return nil
}

func TestFeishuStoredBaselineSurvivesServiceRestarts(t *testing.T) {
	clock := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	var handler *Handler
	var managementCalls atomic.Int64
	_, _, repo := startDashboardTestServer(t, func(writer http.ResponseWriter, _ *http.Request) {
		managementCalls.Add(1)
		writer.WriteHeader(http.StatusInternalServerError)
	}, func(current *Handler) {
		handler = current
		current.nowFn = func() time.Time { return clock }
	})
	seedAuthFile(t, repo, "fixture-codex", "codex-fixture", "codex", false, false, clock)
	seedAuthFile(t, repo, "fixture-unsupported", "unsupported-fixture", "gemini-cli", false, false, clock)
	seedAuthFile(t, repo, "fixture-disabled", "disabled-fixture", "claude", true, false, clock)
	sender := &monitorSender{}
	store := monitorStore{feishuStore: feishuStore{repo: repo}, saves: make(chan struct{}, 2)}
	runStartup := func(wantSends int64) {
		t.Helper()
		secret := "synthetic-app-secret"
		service := feishu.NewService(feishu.Options{
			Web: feishu.Config{
				Enabled: true, AppID: "synthetic-app", ChatID: "synthetic-chat", Timezone: "UTC",
				QuietStart: "23:00", QuietEnd: "08:00", MonitorDownAfter: 30 * time.Minute,
			},
			Overlay: feishu.Overlay{AppSecret: &secret}, Store: store,
			Source: handler.BuildFeishuReportSource(), Now: func() time.Time { return clock },
			SenderFactory: func(feishu.Resolved) feishu.Sender { return sender },
			SourceFactory: func(feishu.Resolved) feishu.EventSource { return monitorEvents{} },
			EvalInterval:  time.Hour,
		})
		if err := service.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		defer service.Stop()
		deadline := time.NewTimer(5 * time.Second)
		defer deadline.Stop()
		// The second save acknowledges delivery; waiting for it also makes the
		// no-send case observable without polling or a fixed sleep.
		for i := 0; i < 2; i++ {
			select {
			case <-store.saves:
			case <-deadline.C:
				t.Fatal("startup tick did not persist its delivery outcome")
			}
		}
		if sends := sender.sends.Load(); sends != wantSends {
			t.Fatalf("sent %d cards, want %d", sends, wantSends)
		}
	}
	runStartup(0)
	clock = clock.Add(30 * time.Minute)
	runStartup(0)
	clock = clock.Add(time.Second)
	runStartup(1)
	clock = clock.Add(time.Minute)
	runStartup(1)
	runtime, err := repo.LoadFeishuRuntime(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(runtime.Tracks) != 1 || runtime.Tracks[0].Provider != "codex" ||
		len(runtime.Pending) != 1 || runtime.Pending[0].Kind != feishu.AlertMonitorDown ||
		len(runtime.Notifications) != 1 || runtime.Notifications[0].State != feishu.NotifySent {
		t.Fatalf("persisted baseline/episode/outbox = %+v", runtime)
	}
	if managementCalls.Load() != 0 {
		t.Fatal("the stored report contacted the upstream management service")
	}
}

package automation

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

// insertObservation records one active observation exactly as the quota refresh path
// would, so the test can run the real timeline query the auto-manager reads.
func insertObservation(t *testing.T, repo *repository.Repository, observation quota.ActiveObservation) {
	t.Helper()
	var reset any
	if observation.ResetAtMS != nil {
		reset = *observation.ResetAtMS
	}
	if _, err := repo.SQL().ExecContext(context.Background(), `
		INSERT INTO quota_observations (
			auth_index, provider, window_kind, scope, model, window_minutes, used_percent,
			reset_at_ms, status, observed_at_ms, event_key, created_at_ms, source
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, '', ?, ?, ?, 'active')`,
		observation.AuthIndex, observation.Provider, observation.WindowKind, observation.Scope,
		observation.Model, observation.WindowMinutes, observation.UsedPercent, reset,
		observation.ObservedAtMS, observation.EventKey, observation.ObservedAtMS); err != nil {
		t.Fatal(err)
	}
}

// TestCodexEvidenceChainDisablesOnlyOnRealWeeklyExhaustion runs the whole chain - Codex
// payload parsing, active observation recording, the timeline query and the pure decision -
// to pin the security contract at the point where it matters. A global rate-limit flag, a
// window that published no reading, and a non-finite or out-of-range reading must never
// become account-level weekly exhaustion evidence; a true 0 is recorded as the measured 0 it
// is and a true 100 with a future reset still disconnects the credential.
func TestCodexEvidenceChainDisablesOnlyOnRealWeeklyExhaustion(t *testing.T) {
	now := time.UnixMilli(1_000_000_000_000)
	nowMS := now.UnixMilli()
	resetAt := now.Add(48 * time.Hour).UnixMilli()
	resetAfterSeconds := strconv.FormatInt(int64(48*time.Hour/time.Second), 10)

	cases := []struct {
		name         string
		weeklyUsed   string // a JSON fragment for the weekly window, empty when it published no reading
		wantEvidence bool
		wantUsed     float64
		wantDisable  bool
	}{
		{"a global limit flag with no weekly reading", ``, false, 0, false},
		{"a weekly window one over the scale", `"used_percent": 101,`, false, 0, false},
		{"a negative weekly window", `"used_percent": -1,`, false, 0, false},
		{"a true zero weekly reading", `"used_percent": 0,`, true, 0, false},
		{"a true hundred weekly reading", `"used_percent": 100,`, true, 100, true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			payload := []byte(`{
				"plan_type": "plus",
				"rate_limit": {
					"allowed": false,
					"limit_reached": true,
					"primary_window": {"used_percent": 10, "limit_window_seconds": 18000, "reset_after_seconds": 3600},
					"secondary_window": {` + testCase.weeklyUsed + ` "limit_window_seconds": 604800, "reset_after_seconds": ` + resetAfterSeconds + `}
				}
			}`)
			_, windows, _, err := quota.ParseCodexUsage(payload, nowMS)
			if err != nil {
				t.Fatalf("ParseCodexUsage failed: %v", err)
			}
			observations := quota.ActiveObservationsFromQuota(&quota.NormalizedQuota{
				AuthIndex: "idx-1", Provider: "codex", ObservedAtMS: nowMS, Windows: windows,
			})
			repo := automationRepo(t)
			for _, observation := range observations {
				insertObservation(t, repo, observation)
			}
			evidence, err := repo.LatestStandardWeeklyWindows(context.Background(), []string{"idx-1"})
			if err != nil {
				t.Fatal(err)
			}
			window, found := evidence["idx-1"]
			if found != testCase.wantEvidence {
				t.Fatalf("weekly evidence = %+v, found=%v, want found=%v", window, found, testCase.wantEvidence)
			}
			var evidencePtr *repository.AutomationWeeklyWindow
			if found {
				if window.UsedPercent != testCase.wantUsed {
					t.Fatalf("weekly evidence used = %v, want %v", window.UsedPercent, testCase.wantUsed)
				}
				evidencePtr = &window
			}
			decision := Decide(adoptedAccount(), evidencePtr, liveCredential(), now, DefaultPolicyConfig())
			if (decision.Kind == DecisionDisable) != testCase.wantDisable {
				t.Fatalf("decision = %+v, want disable=%v", decision, testCase.wantDisable)
			}
			if testCase.wantDisable && decision.ResetAtMS != resetAt {
				t.Fatalf("disable reset = %d, want %d", decision.ResetAtMS, resetAt)
			}
		})
	}
}

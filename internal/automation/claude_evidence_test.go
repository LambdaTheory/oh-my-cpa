package automation

import (
	"context"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

// TestClaudeEvidenceChainDisablesOnlyOnRealWeeklyExhaustion runs the whole chain - Claude
// payload parsing, active observation recording, the real timeline query and the pure
// decision. A non-finite or out-of-range utilization must never become account-level weekly
// exhaustion evidence; a true 0 is recorded as the measured 0 it is, a true 100 with a future
// reset still disconnects the credential, and a model-scoped weekly window never elevates the
// account even at 100%.
func TestClaudeEvidenceChainDisablesOnlyOnRealWeeklyExhaustion(t *testing.T) {
	now := time.UnixMilli(1_000_000_000_000)
	nowMS := now.UnixMilli()
	resetAt := now.Add(48 * time.Hour).UnixMilli()
	reset := now.Add(48 * time.Hour).UTC().Format(time.RFC3339)

	cases := []struct {
		name         string
		sevenDayUtil string // the raw JSON value for seven_day.utilization
		wantEvidence bool
		wantUsed     float64
		wantDisable  bool
	}{
		{"a utilization one over the scale", `101`, false, 0, false},
		{"a negative utilization", `-1`, false, 0, false},
		{"a non-numeric utilization", `"not-a-number"`, false, 0, false},
		{"a true zero", `0`, true, 0, false},
		{"a true hundred", `100`, true, 100, true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			payload := []byte(`{"five_hour":{"utilization":10,"resets_at":"` + reset + `"},` +
				`"seven_day":{"utilization":` + testCase.sevenDayUtil + `,"resets_at":"` + reset + `"}}`)
			windows, _, err := quota.ParseClaudeUsage(payload, nowMS)
			if err != nil {
				t.Fatalf("ParseClaudeUsage failed: %v", err)
			}
			observations := quota.ActiveObservationsFromQuota(&quota.NormalizedQuota{
				AuthIndex: "idx-1", Provider: "claude", ObservedAtMS: nowMS, Windows: windows,
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
			decision := Decide(claudeAccount(), evidencePtr, claudeCredential(), now, DefaultPolicyConfig())
			if (decision.Kind == DecisionDisable) != testCase.wantDisable {
				t.Fatalf("decision = %+v, want disable=%v", decision, testCase.wantDisable)
			}
			if testCase.wantDisable && decision.ResetAtMS != resetAt {
				t.Fatalf("disable reset = %d, want %d", decision.ResetAtMS, resetAt)
			}
		})
	}

	// A model-scoped seven-day window is not the account-level weekly window: even at 100% with
	// a future reset it must not become timeline evidence for the account or disable it.
	t.Run("a model-scoped weekly window does not elevate the account", func(t *testing.T) {
		payload := []byte(`{"five_hour":{"utilization":10,"resets_at":"` + reset + `"},` +
			`"seven_day_sonnet":{"utilization":100,"resets_at":"` + reset + `"}}`)
		windows, _, err := quota.ParseClaudeUsage(payload, nowMS)
		if err != nil {
			t.Fatalf("ParseClaudeUsage failed: %v", err)
		}
		observations := quota.ActiveObservationsFromQuota(&quota.NormalizedQuota{
			AuthIndex: "idx-1", Provider: "claude", ObservedAtMS: nowMS, Windows: windows,
		})
		repo := automationRepo(t)
		for _, observation := range observations {
			insertObservation(t, repo, observation)
		}
		evidence, err := repo.LatestStandardWeeklyWindows(context.Background(), []string{"idx-1"})
		if err != nil {
			t.Fatal(err)
		}
		if window, found := evidence["idx-1"]; found {
			t.Fatalf("a model-scoped weekly window elevated the account: %+v", window)
		}
		if decision := Decide(adoptedAccount(), nil, liveCredential(), now, DefaultPolicyConfig()); decision.Kind == DecisionDisable {
			t.Fatalf("a model-scoped weekly window must not disable the account: %+v", decision)
		}
	})
}

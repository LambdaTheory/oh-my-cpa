package operations

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/capability"
	"github.com/oh-my-cpa/oh-my-cpa/internal/domain"
	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

func TestResetAdviceAnnouncementRules(t *testing.T) {
	for _, test := range []struct {
		name, confidence, provider, scope, target string
		hours                                     int64
		hasTime, hasBalance, isDisabled           bool
		wantVerdict                               string
		wantCount                                 int
	}{
		{"confirmed", "confirmed", "codex", "all", "", 2, true, true, false, "use_before_expiry", 1},
		{"probable", "probable", "codex", "all", "", 2, true, true, false, "use_before_expiry", 1},
		{"unverified", "unverified", "codex", "all", "", 2, true, true, false, "no_action", 1},
		{"untimed", "confirmed", "codex", "all", "", 2, false, true, false, "no_action", 1},
		{"expired", "confirmed", "codex", "all", "", 0, true, true, false, "no_action", 0},
		{"outside horizon", "confirmed", "codex", "all", "", 73, true, true, false, "no_action", 1},
		{"horizon boundary", "confirmed", "codex", "all", "", 72, true, true, false, "use_before_expiry", 1},
		{"other provider", "confirmed", "claude", "all", "", 2, true, true, false, "no_action", 0},
		{"same credential", "confirmed", "codex", "credential", "private-auth", 2, true, true, false, "use_before_expiry", 1},
		{"other credential", "confirmed", "codex", "credential", "other", 2, true, true, false, "no_action", 0},
		{"unknown model", "confirmed", "codex", "model", "model-x", 2, true, true, false, "no_action", 0},
		{"zero balance", "confirmed", "codex", "all", "", 2, true, false, false, "no_action", 1},
		{"disabled", "confirmed", "codex", "all", "", 2, true, true, true, "no_action", 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, ctx, now := resetAdviceFixture(t)
			resource := predictionResource("private-auth", "example", "codex")
			if test.isDisabled {
				resource.Details.Disabled = true
			}
			seedPredictionResources(t, service.Repo, []domain.DiscoveredResource{resource})
			credits := `{"available_count":0,"credits":[]}`
			if test.hasBalance {
				credits = fmt.Sprintf(`{"available_count":1,"credits":[{"status":"available","expires_at_ms":%d}]}`, now+120*time.Hour.Milliseconds())
			}
			seedResetAdviceSnapshot(t, service.Repo, "private-auth", "codex", now, credits, "")
			reset := now + test.hours*time.Hour.Milliseconds()
			input := repository.ResetAnnouncementInput{Provider: test.provider, Source: "manual", Scope: test.scope, Confidence: test.confidence, OriginalText: "Synthetic public notice"}
			if test.hasTime {
				input.ExpectedResetAtMS = &reset
			}
			if test.scope == "credential" {
				input.AuthIndex = test.target
			}
			if test.scope == "model" {
				input.Model = test.target
			}
			if _, err := service.Repo.CreateResetAnnouncement(ctx, input, now-1); err != nil {
				t.Fatal(err)
			}
			result, err := service.ResetAdvice(ctx, ResetAdviceInput{})
			if err != nil || len(result.Items) != 1 {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			item := result.Items[0]
			if item.Verdict != test.wantVerdict || len(item.Announcements) != test.wantCount {
				t.Fatalf("item=%+v", item)
			}
			if item.LocalEvidence.Verdict != quota.ResetAdviceNoAction {
				t.Fatalf("local evidence overwritten: %+v", item)
			}
			if test.wantVerdict == quota.ResetAdviceUseBeforeExpiry {
				entry := item.Announcements[0]
				if entry.Source != "manual" || entry.Confidence != test.confidence || !entry.InfluencesAdvice || !entry.HasConflict || !slices.Contains(item.Reasons, "announcement_reset_soon") {
					t.Fatalf("missing provenance: %+v", item)
				}
				if *item.LocalEvidence.QuotaResetAtMS != now+time.Hour.Milliseconds() || *entry.ExpectedResetAtMS != reset {
					t.Fatalf("conflicting readings lost: %+v", item)
				}
			}
			if test.name == "zero balance" || test.name == "disabled" {
				if !slices.Contains(item.Reasons, "notice_without_local_capacity") {
					t.Fatalf("missing capacity refusal: %+v", item)
				}
			}
		})
	}
}

func TestResetAdviceAnnouncementAdapterParityAndEmergency(t *testing.T) {
	service, ctx, now := resetAdviceFixture(t)
	seedPredictionResources(t, service.Repo, []domain.DiscoveredResource{predictionResource("private-auth", "vincent@example.com", "codex")})
	credits := fmt.Sprintf(`{"available_count":1,"credits":[{"status":"available","expires_at_ms":%d}]}`, now+120*time.Hour.Milliseconds())
	seedResetAdviceSnapshot(t, service.Repo, "private-auth", "codex", now, credits, "")
	windows := fmt.Sprintf(`[{"id":"five_hour","kind":"five_hour","scope":"standard","used_percent":95,"reset_at_ms":%d},{"id":"model-x","scope":"model","model":"model-x","used_percent":20,"reset_at_ms":%d}]`, now+time.Hour.Milliseconds(), now+time.Hour.Milliseconds())
	if _, err := service.Repo.SQL().ExecContext(ctx, "UPDATE quota_snapshots SET windows_json = ?", windows); err != nil {
		t.Fatal(err)
	}
	reset := now + 2*time.Hour.Milliseconds()
	for _, scope := range []string{"credential", "model"} {
		input := repository.ResetAnnouncementInput{Provider: "codex", Source: "manual", Scope: scope, Confidence: "confirmed", ExpectedResetAtMS: &reset, OriginalText: "Synthetic public notice"}
		if scope == "credential" {
			input.AuthIndex = "private-auth"
		} else {
			input.Model = "model-x"
		}
		if _, err := service.Repo.CreateResetAnnouncement(ctx, input, now); err != nil {
			t.Fatal(err)
		}
	}
	registry := capability.NewRegistry()
	if err := service.Register(registry); err != nil {
		t.Fatal(err)
	}
	executor := &capability.Executor{Registry: registry, Store: repository.AgentStore{Repo: service.Repo}, Authorize: func(_ context.Context, _, _ string) bool { return true }}
	var first ResetAdviceItem
	for _, principal := range []capability.Principal{{ID: "agent", Adapter: "agent", IsAdmin: true}, {ID: "readonly", Adapter: "mcp", IsReadOnly: true, Allowed: map[string]bool{"reset_advice": true}}} {
		result, err := executor.Invoke(ctx, principal, "reset_advice", json.RawMessage(`{}`), "")
		if err != nil || result.Status != "success" {
			t.Fatalf("result=%+v err=%v", result, err)
		}
		var output ResetAdviceResult
		if err := json.Unmarshal(result.Data, &output); err != nil {
			t.Fatal(err)
		}
		item := output.Items[0]
		if item.Verdict != "use_before_expiry" || item.LocalEvidence.Verdict != "keep_for_emergency" || *item.UsedPercent != 95 || len(item.Announcements) != 2 {
			t.Fatalf("item=%+v", item)
		}
		if principal.IsReadOnly {
			if strings.Contains(string(result.Data), "auth_index") || strings.Contains(string(result.Data), "private-auth") {
				t.Fatalf("identity leaked: %s", result.Data)
			}
			item.AuthIndex, item.CredentialLabel = first.AuthIndex, first.CredentialLabel
			if !reflect.DeepEqual(first, item) {
				t.Fatalf("adapter mismatch: %+v vs %+v", first, item)
			}
		} else {
			first = item
		}
	}
	result, err := service.ResetAdvice(capability.WithAnchor(ctx, now+3*time.Hour.Milliseconds()), ResetAdviceInput{})
	if err != nil || result.Items[0].Verdict != "unknown" || len(result.Items[0].Announcements) != 0 {
		t.Fatalf("expired=%+v err=%v", result, err)
	}
}

func TestResetAdviceAnnouncementCannotSupplyMissingCapacity(t *testing.T) {
	service, ctx, now := resetAdviceFixture(t)
	seedPredictionResources(t, service.Repo, []domain.DiscoveredResource{predictionResource("missing", "Synthetic", "codex")})
	reset := now + time.Hour.Milliseconds()
	if _, err := service.Repo.CreateResetAnnouncement(ctx, repository.ResetAnnouncementInput{Provider: "codex", Source: "manual", Scope: "all", Confidence: "confirmed", ExpectedResetAtMS: &reset, OriginalText: "Synthetic notice"}, now); err != nil {
		t.Fatal(err)
	}
	result, err := service.ResetAdvice(ctx, ResetAdviceInput{})
	if err != nil || len(result.Items) != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	item := result.Items[0]
	if item.Verdict != "unknown" || item.RemainingResets != nil || len(item.Announcements) != 1 || item.Announcements[0].InfluencesAdvice || !slices.Contains(item.Reasons, "notice_without_local_capacity") {
		t.Fatalf("missing capacity became advice: %+v", item)
	}
}

func TestResetAdviceReadsAnnouncementsBeyondFirstPage(t *testing.T) {
	service, ctx, now := resetAdviceFixture(t)
	seedPredictionResources(t, service.Repo, []domain.DiscoveredResource{predictionResource("target", "Synthetic", "codex")})
	credits := fmt.Sprintf(`{"available_count":1,"credits":[{"status":"available","expires_at_ms":%d}]}`, now+120*time.Hour.Milliseconds())
	seedResetAdviceSnapshot(t, service.Repo, "target", "codex", now, credits, "")
	reset := now + time.Hour.Milliseconds()
	for index := 0; index < 101; index++ {
		provider := "claude"
		if index == 0 {
			provider = "codex"
		}
		if _, err := service.Repo.CreateResetAnnouncement(ctx, repository.ResetAnnouncementInput{Provider: provider, Source: "manual", Scope: "all", Confidence: "confirmed", ExpectedResetAtMS: &reset, OriginalText: "Synthetic notice"}, now-int64(101-index)); err != nil {
			t.Fatal(err)
		}
	}
	result, err := service.ResetAdvice(ctx, ResetAdviceInput{})
	if err != nil || len(result.Items) != 1 || result.Items[0].Verdict != "use_before_expiry" || len(result.Items[0].Announcements) != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

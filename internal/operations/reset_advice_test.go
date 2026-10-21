package operations

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/capability"
	"github.com/oh-my-cpa/oh-my-cpa/internal/cpa/management"
	"github.com/oh-my-cpa/oh-my-cpa/internal/domain"
	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

func resetAdviceFixture(t *testing.T) (*Service, context.Context, int64) {
	t.Helper()
	db, err := repository.Open(context.Background(), filepath.Join(t.TempDir(), "reset-advice.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	now := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC).UnixMilli()
	service := &Service{Repo: repository.New(db),
		Client: func(context.Context) (*management.Client, error) { t.Fatal("advice requested CPA"); return nil, nil },
		Quotas: func(context.Context) ([]Quota, error) { t.Fatal("advice requested live quotas"); return nil, nil },
		RefreshQuota: func(context.Context, string) (Quota, error) {
			t.Fatal("advice refreshed upstream quota")
			return Quota{}, nil
		},
	}
	return service, capability.WithAnchor(context.Background(), now), now
}

func seedResetAdviceSnapshot(t *testing.T, repo *repository.Repository, authIndex, provider string, now int64, credits, grants string) {
	t.Helper()
	used, reset, period := 30.0, now+time.Hour.Milliseconds(), 5.0
	windows, err := json.Marshal([]quota.QuotaWindow{{ID: "five_hour", Kind: "five_hour", Scope: "standard", UsedPercent: &used, ResetAtMS: &reset, PeriodHours: &period}})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveQuotaSnapshot(context.Background(), repository.QuotaSnapshotRecord{AuthIndex: authIndex, Provider: provider, Status: "healthy", ObservedAtMS: now, WindowsJSON: string(windows), ResetCreditsJSON: credits, ResetGrantsJSON: grants}); err != nil {
		t.Fatal(err)
	}
}

func TestResetAdviceSharedReadCapabilityAndPrivacy(t *testing.T) {
	service, ctx, now := resetAdviceFixture(t)
	seedPredictionResources(t, service.Repo, []domain.DiscoveredResource{predictionResource("auth-private", "vincent@example.com", "codex")})
	credits := fmt.Sprintf(`{"available_count":1,"credits":[{"id":"private-credit","status":"available","expires_at_ms":%d}]}`, now+48*time.Hour.Milliseconds())
	seedResetAdviceSnapshot(t, service.Repo, "auth-private", "codex", now, credits, "")
	registry := capability.NewRegistry()
	if err := service.Register(registry); err != nil {
		t.Fatal(err)
	}
	executor := &capability.Executor{Registry: registry, Store: repository.AgentStore{Repo: service.Repo}, Authorize: func(context.Context, string, string) bool { return true }}
	for _, principal := range []capability.Principal{
		{ID: "agent", Adapter: "agent", IsAdmin: true},
		{ID: "readonly", Adapter: "mcp", IsReadOnly: true, Allowed: map[string]bool{"reset_advice": true}},
	} {
		definition, err := registry.Lookup("reset_advice", principal)
		if err != nil || definition.Permission != "read" || definition.Risk != "low" {
			t.Fatalf("definition=%+v err=%v", definition, err)
		}
		result, err := executor.Invoke(ctx, principal, "reset_advice", json.RawMessage(`{"provider":"codex","window_kind":"five_hour"}`), "")
		if err != nil || result.Status != "success" {
			t.Fatalf("result=%+v err=%v", result, err)
		}
		var output ResetAdviceResult
		if err := json.Unmarshal(result.Data, &output); err != nil || len(output.Items) != 1 {
			t.Fatalf("output=%+v err=%v", output, err)
		}
		item := output.Items[0]
		if !strings.Contains(string(result.Data), `"announcements":[]`) || !strings.Contains(string(result.Data), `"cooldown_active":null`) {
			t.Fatalf("missing evidence fields: %s", result.Data)
		}
		if item.Verdict != quota.ResetAdviceUseBeforeExpiry || item.Evidence != "projection" || item.RemainingResets == nil || *item.RemainingResets != 1 {
			t.Fatalf("item=%+v", item)
		}
		if strings.Contains(string(result.Data), "private-credit") {
			t.Fatal("grant identity leaked")
		}
		if principal.IsReadOnly {
			if strings.Contains(string(result.Data), "auth_index") || strings.Contains(string(result.Data), "auth-private") || item.CredentialLabel != "vin***@example.com" {
				t.Fatalf("readonly output=%s", result.Data)
			}
		} else if item.AuthIndex != "auth-private" || item.CredentialLabel != "vincent@example.com" {
			t.Fatalf("agent item=%+v", item)
		}
	}
	service.ResetAdviceExpiryWindow = 24 * time.Hour
	result, err := service.ResetAdvice(ctx, ResetAdviceInput{AuthIndex: "auth-private"})
	if err != nil || result.Items[0].Verdict != quota.ResetAdviceNoAction {
		t.Fatalf("configured horizon=%+v err=%v", result, err)
	}
}

func TestResetAdviceMissingAndMalformedReadingsStayUnknown(t *testing.T) {
	service, ctx, now := resetAdviceFixture(t)
	resources := []domain.DiscoveredResource{}
	for _, entry := range []struct{ id, provider, credits, grants string }{
		{"missing-snapshot", "codex", "", ""},
		{"missing-credits", "codex", "", ""},
		{"missing-count", "codex", `{"credits":[]}`, ""},
		{"missing-grant-count", "claude", "", `[{"id":"grant","clears":["five_hour"]}]`},
		{"missing-grants", "claude", "", `[]`},
	} {
		resources = append(resources, predictionResource(entry.id, entry.id, entry.provider))
		if entry.id != "missing-snapshot" {
			seedResetAdviceSnapshot(t, service.Repo, entry.id, entry.provider, now, entry.credits, entry.grants)
		}
	}
	seedPredictionResources(t, service.Repo, resources)
	result, err := service.ResetAdvice(ctx, ResetAdviceInput{})
	if err != nil || len(result.Items) != len(resources) {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	for _, item := range result.Items {
		if item.Verdict != quota.ResetAdviceUnknown || item.Evidence != "unknown" || item.RemainingResets != nil || len(item.Reasons) == 0 {
			t.Fatalf("item=%+v", item)
		}
	}
}

func TestResetAdvicePassiveQuotaDoesNotRenewEntitlements(t *testing.T) {
	service, ctx, now := resetAdviceFixture(t)
	seedPredictionResources(t, service.Repo, []domain.DiscoveredResource{predictionResource("claude", "account", "claude")})
	grants := fmt.Sprintf(`[{"id":"grant","resets_total":3,"resets_left":2,"ends_at_ms":%d,"clears":["five_hour"]}]`, now+48*time.Hour.Milliseconds())
	seedResetAdviceSnapshot(t, service.Repo, "claude", "claude", now-time.Minute.Milliseconds(), "", grants)
	_, err := service.Repo.SQL().ExecContext(ctx, `INSERT INTO quota_observations (auth_index, provider, window_kind, scope, model, window_minutes, used_percent, reset_at_ms, status, observed_at_ms, event_key, created_at_ms, source) VALUES ('claude','claude','five_hour','standard','',300,95,?,'limited',?,'reset-advice',?,'passive')`, now+time.Hour.Milliseconds(), now, now)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.ResetAdvice(ctx, ResetAdviceInput{})
	if err != nil || len(result.Items) != 1 || result.Items[0].Verdict != quota.ResetAdviceKeepForEmergency || *result.Items[0].UsedPercent != 95 || *result.Items[0].RemainingResets != 2 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	result, err = service.ResetAdvice(capability.WithAnchor(ctx, now+3*time.Hour.Milliseconds()), ResetAdviceInput{})
	if err != nil || result.Items[0].Verdict != quota.ResetAdviceUnknown || !slices.Contains(result.Items[0].Reasons, "reset_reading_stale") {
		t.Fatalf("stale=%+v err=%v", result, err)
	}
}

func TestResetAdviceBoundsAndFilters(t *testing.T) {
	service, ctx, _ := resetAdviceFixture(t)
	resources := []domain.DiscoveredResource{}
	for i := 0; i < 120; i++ {
		resources = append(resources, predictionResource(fmt.Sprintf("auth-%03d", i), strings.Repeat("label", 50), "codex"))
	}
	seedPredictionResources(t, service.Repo, resources)
	for _, limit := range []int{0, 1, 100} {
		result, err := service.ResetAdvice(ctx, ResetAdviceInput{Limit: limit})
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(result)
		if err != nil || len(raw) > capability.MAX_PAYLOAD_BYTES || len(result.Items) == 0 || len(result.Items) > 100 || len(result.Items)+result.Omitted != 120 {
			t.Fatalf("bounds items=%d omitted=%d bytes=%d err=%v", len(result.Items), result.Omitted, len(raw), err)
		}
		if limit == 1 && len(result.Items) != 1 {
			t.Fatal("limit ignored")
		}
	}
	for _, input := range []ResetAdviceInput{{Limit: -1}, {Limit: 101}, {Provider: "INVALID"}, {WindowKind: "bad kind"}} {
		if _, err := service.ResetAdvice(ctx, input); err == nil || err.Error() != "invalid_parameters" {
			t.Fatalf("input=%+v err=%v", input, err)
		}
	}
	if _, err := service.ResetAdvice(capability.WithReadOnly(ctx, true), ResetAdviceInput{AuthIndex: "auth-000"}); err == nil || err.Error() != "invalid_parameters" {
		t.Fatalf("readonly filter err=%v", err)
	}
	result, err := service.ResetAdvice(ctx, ResetAdviceInput{Provider: "claude"})
	if err != nil || len(result.Items) != 0 {
		t.Fatalf("provider filter=%+v err=%v", result, err)
	}
}

func TestResetAdviceOneConclusionPerCredential(t *testing.T) {
	service, ctx, now := resetAdviceFixture(t)
	seedPredictionResources(t, service.Repo, []domain.DiscoveredResource{predictionResource("codex", "account", "codex")})
	credits := fmt.Sprintf(`{"available_count":1,"credits":[{"status":"available","expires_at_ms":%d}]}`, now+time.Hour.Milliseconds())
	windows := fmt.Sprintf(`[{"id":"five_hour","kind":"five_hour","scope":"standard","used_percent":95,"reset_at_ms":%d},{"id":"seven_day","kind":"weekly","scope":"standard","used_percent":20,"reset_at_ms":%d}]`, now+time.Hour.Milliseconds(), now+time.Hour.Milliseconds())
	if err := service.Repo.SaveQuotaSnapshot(ctx, repository.QuotaSnapshotRecord{AuthIndex: "codex", Provider: "codex", Status: "healthy", ObservedAtMS: now, WindowsJSON: windows, ResetCreditsJSON: credits}); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"", "five_hour", "weekly"} {
		result, err := service.ResetAdvice(ctx, ResetAdviceInput{WindowKind: kind})
		if err != nil || len(result.Items) != 1 || result.Omitted != 0 {
			t.Fatalf("kind=%q result=%+v err=%v", kind, result, err)
		}
		item := result.Items[0]
		if item.Verdict != quota.ResetAdviceKeepForEmergency || item.WindowKind != "five_hour" || item.UsedPercent == nil || *item.UsedPercent != 95 || item.RemainingResets == nil || *item.RemainingResets != 1 {
			t.Fatalf("kind=%q item=%+v", kind, item)
		}
	}
}

func TestResetAdviceCooldownEvidence(t *testing.T) {
	service, ctx, now := resetAdviceFixture(t)
	seedPredictionResources(t, service.Repo, []domain.DiscoveredResource{predictionResource("codex", "account", "codex")})
	if _, err := service.Repo.SQL().ExecContext(ctx, `INSERT INTO cpa_instances (id, name, base_url, usage_addr, management_key_ciphertext, management_key_nonce, status, created_at, updated_at) VALUES ('default', 'Default', 'http://127.0.0.1:18317', '127.0.0.1:18317', x'00', x'00', 'unknown', 0, 0)`); err != nil {
		t.Fatal(err)
	}
	_, err := service.Repo.SQL().ExecContext(ctx, `INSERT INTO error_events (instance_id, event_key, provider, model, auth_index, status_code, body, retryable, auth_status, auth_disabled, auth_unavailable, quota_exceeded, quota_reason, next_recover_at_ms, timestamp_ms, created_at_ms) VALUES ('default', 'reset-cooldown', 'codex', '', 'codex', 429, '', 1, 'active', 0, 0, 1, 'quota', ?, ?, ?)`, now+time.Minute.Milliseconds(), now, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		at   int64
		want bool
	}{{now, true}, {now + time.Minute.Milliseconds(), false}} {
		result, err := service.ResetAdvice(capability.WithAnchor(ctx, test.at), ResetAdviceInput{})
		if err != nil || len(result.Items) != 1 {
			t.Fatalf("result=%+v err=%v", result, err)
		}
		item := result.Items[0]
		if item.CooldownActive == nil || *item.CooldownActive != test.want {
			t.Fatalf("cooldown=%v want=%v", item.CooldownActive, test.want)
		}
		raw, err := json.Marshal(item)
		if err != nil || !strings.Contains(string(raw), fmt.Sprintf(`"cooldown_active":%t`, test.want)) {
			t.Fatalf("wire=%s err=%v", raw, err)
		}
	}
}

func TestClaudeResetAdvicePersistedReadingStates(t *testing.T) {
	const grant = `{"id":"weekly","resets_total":2,"resets_left":1,"ends_at":"2026-10-22T00:00:00Z","clears":["five_hour"]}`
	for _, tc := range []struct {
		name, body, reason string
		wantCount          bool
	}{
		{"eligible", `{"cedar_ember":{"eligible":true,"grants":[` + grant + `]}}`, "reset_expiring_soon", true},
		{"eligible omitted", `{"cedar_ember":{"grants":[` + grant + `]}}`, "reset_expiring_soon", true},
		{"ineligible", `{"cedar_ember":{"eligible":false,"grants":[` + grant + `]}}`, "reset_expiring_soon", true},
		{"not provided", `{"cedar_ember":{"grants":[]}}`, "reset_not_provided", false},
		{"eligible empty", `{"cedar_ember":{"eligible":true,"grants":[]}}`, "reset_not_provided", false},
		{"eligible omitted grants", `{"cedar_ember":{"eligible":true}}`, "reset_not_provided", false},
		{"eligible null grants", `{"cedar_ember":{"eligible":true,"grants":null}}`, "reset_not_provided", false},
		{"absent", `{}`, "reset_not_provided", false},
		{"failed", `{`, "reset_reading_failed", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, ctx, now := resetAdviceFixture(t)
			seedPredictionResources(t, service.Repo, []domain.DiscoveredResource{predictionResource("synthetic-claude", "Synthetic Claude", "claude")})
			grants, status := quota.ParseClaudeResetGrants([]byte(tc.body))
			raw, _ := json.Marshal(grants)
			seedResetAdviceSnapshot(t, service.Repo, "synthetic-claude", "claude", now, "", string(raw))
			snapshots, err := service.Repo.GetLatestQuotaSnapshots(ctx, []string{"synthetic-claude"})
			if err != nil {
				t.Fatal(err)
			}
			snapshot := snapshots["synthetic-claude"]
			snapshot.ID = ""
			snapshot.ResetGrantsStatus = string(status)
			if err := service.Repo.SaveQuotaSnapshot(ctx, snapshot); err != nil {
				t.Fatal(err)
			}
			result, err := service.ResetAdvice(ctx, ResetAdviceInput{})
			if err != nil || len(result.Items) != 1 {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			item := result.Items[0]
			if !slices.Contains(item.Reasons, tc.reason) {
				t.Fatalf("item=%+v", item)
			}
			if tc.wantCount {
				expiry := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC).UnixMilli()
				if item.Verdict != quota.ResetAdviceUseBeforeExpiry || item.RemainingResets == nil || *item.RemainingResets != 1 || item.EarliestExpiryMS == nil || *item.EarliestExpiryMS != expiry {
					t.Fatalf("item=%+v", item)
				}
			} else if item.Verdict != quota.ResetAdviceUnknown || item.RemainingResets != nil {
				t.Fatalf("item=%+v", item)
			}
		})
	}
}

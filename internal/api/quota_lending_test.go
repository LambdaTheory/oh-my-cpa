package api

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/cpa/management"
	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

const lendingContractNowMS = int64(1_763_000_000_000)
const lendingContractDayMS = int64(24 * 60 * 60 * 1000)

// lendingContractRoster is the fake gateway's credential list: two Codex seats on
// one plan, plus a provider whose plan the console cannot read and that still
// serves one of the pool's models.
const lendingContractRoster = `{
	"files": [
		{"id": "file-a", "name": "codex-a.json", "auth_index": "codex-a", "type": "codex", "provider": "codex", "disabled": false, "runtime_only": false, "priority": 0, "email": "ops@acme-labs.example", "models": [{"id": "gpt-5-codex", "display_name": "GPT-5 Codex"}, {"id": "gpt-5", "display_name": "GPT-5"}]},
		{"id": "file-b", "name": "codex-b.json", "auth_index": "codex-b", "type": "codex", "provider": "codex", "disabled": false, "runtime_only": false, "priority": 0, "models": [{"id": "gpt-5-codex", "display_name": "GPT-5 Codex"}]},
		{"id": "file-x", "name": "xai-a.json", "auth_index": "xai-a", "type": "xai", "provider": "xai", "disabled": false, "runtime_only": false, "priority": 4, "email": "research@acme-labs.example", "models": [{"id": "gpt-5-codex", "display_name": "GPT-5 Codex"}]}
	]
}`

func startLendingContractServer(t *testing.T) (quotaLendingContract, func()) {
	t.Helper()
	now := time.UnixMilli(lendingContractNowMS).UTC()
	var handler *Handler
	client, baseURL, repo := startDashboardTestServer(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v8/management/credentials" {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(lendingContractRoster))
	}, func(appHandler *Handler) {
		handler = appHandler
		appHandler.nowFn = func() time.Time { return now }
	})

	seedLendingContractSnapshot(t, repo, "codex-a", 88, lendingContractNowMS+int64(3.9*float64(lendingContractDayMS)))
	seedLendingContractSnapshot(t, repo, "codex-b", 40, lendingContractNowMS+int64(2.2727*float64(lendingContractDayMS)))

	return quotaLendingContract{client: client, baseURL: baseURL, repo: repo, handler: handler}, func() {}
}

type quotaLendingContract struct {
	client  *http.Client
	baseURL string
	repo    *repository.Repository
	handler *Handler
}

func seedLendingContractSnapshot(t *testing.T, repo *repository.Repository, authIndex string, remaining float64, resetAtMS int64) {
	t.Helper()
	used := 100 - remaining
	period := 168.0
	windows := []quota.QuotaWindow{{
		ID:               "weekly",
		Label:            "Weekly",
		Kind:             "weekly",
		Scope:            "standard",
		UsedPercent:      &used,
		RemainingPercent: &remaining,
		ResetAtMS:        &resetAtMS,
		PeriodHours:      &period,
		ResetAccuracy:    "exact",
		ResetLabel:       "in a few days",
	}}
	windowsJSON, err := json.Marshal(windows)
	if err != nil {
		t.Fatal(err)
	}
	plan := quota.QuotaPlan{PlanType: "pro", PlanLabel: "Pro 200", Tier: "elite"}
	planJSON, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	observedAtMS := lendingContractNowMS - 5*60*1000
	if err := repo.SaveQuotaSnapshot(context.Background(), repository.QuotaSnapshotRecord{
		ID:           "snap-" + authIndex,
		AuthIndex:    authIndex,
		Provider:     "codex",
		Status:       "healthy",
		PlanType:     plan.PlanType,
		PlanTier:     plan.Tier,
		PlanJSON:     string(planJSON),
		WindowsJSON:  string(windowsJSON),
		ObservedAtMS: observedAtMS,
		CreatedAtMS:  observedAtMS,
	}); err != nil {
		t.Fatal(err)
	}
}

func getLendingContractJSON(t *testing.T, client *http.Client, url string, target any) []byte {
	t.Helper()
	response, payload := getJSON(t, client, url)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d: %s", url, response.StatusCode, payload)
	}
	if err := json.Unmarshal(payload, target); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
	return payload
}

func TestQuotaLendingOverviewAndDetailAgree(t *testing.T) {
	contract, cleanup := startLendingContractServer(t)
	defer cleanup()

	var overview struct {
		Quotas  []QuotaItemDTO     `json:"quotas"`
		Lending lendingOverviewDTO `json:"lending"`
	}
	overviewBody := getLendingContractJSON(t, contract.client, contract.baseURL+"/omc/api/v1/management/quota", &overview)

	if overview.Lending.GeneratedAtMS != lendingContractNowMS {
		t.Fatalf("generated_at_ms = %d, want %d", overview.Lending.GeneratedAtMS, lendingContractNowMS)
	}
	if len(overview.Lending.Pools) != 1 {
		t.Fatalf("pools = %+v", overview.Lending.Pools)
	}
	pool := overview.Lending.Pools[0]
	if pool.ID != "codex:pro" || pool.Provider != "codex" || pool.PlanType != "pro" || pool.State != quota.LendingStateAdvised {
		t.Fatalf("pool = %+v", pool)
	}
	if pool.WindowKind != "weekly" || pool.IsBalanced || pool.NeedsChangeCount != 1 || pool.HoldCount != 0 {
		t.Fatalf("pool = %+v", pool)
	}
	if len(pool.Cards) != 2 {
		t.Fatalf("pool cards = %+v", pool.Cards)
	}
	first, second := pool.Cards[0], pool.Cards[1]
	// The later refill ranks first: codex-a refills in 3.9 days, codex-b in 2.27.
	if first.AuthIndex != "codex-a" || first.Role != quota.LendingRoleSuggested || first.Tier != 1 {
		t.Fatalf("first card = %+v, want codex-a in tier 1", first)
	}
	if second.AuthIndex != "codex-b" || second.Role != quota.LendingRoleSuggested || second.Tier != 2 {
		t.Fatalf("second card = %+v, want codex-b in tier 2", second)
	}
	if first.DisplayName != "ops@acme-labs.example" {
		t.Fatalf("first display name = %q, want the account email", first.DisplayName)
	}
	if second.DisplayName != "codex-b.json" {
		t.Fatalf("second display name = %q, want the file name", second.DisplayName)
	}
	if !first.PriorityKnown || !first.PriorityIsDefault || first.Priority == nil || *first.Priority != 0 {
		t.Fatalf("first priority = %+v", first)
	}
	if !first.CanEditPriority || !second.CanEditPriority {
		t.Fatalf("pool cards should be editable: %+v %+v", first, second)
	}
	if first.SuggestedPriority == nil || *first.SuggestedPriority != 0 || first.NeedsChange == nil || *first.NeedsChange {
		t.Fatalf("first suggestion = %+v", first)
	}
	if second.SuggestedPriority == nil || *second.SuggestedPriority != -10 || second.NeedsChange == nil || !*second.NeedsChange {
		t.Fatalf("second suggestion = %+v", second)
	}
	if first.DailyAvailablePercent == nil || *first.DailyAvailablePercent != 22.6 {
		t.Fatalf("first daily = %v, want 22.6", first.DailyAvailablePercent)
	}
	if pool.ValidUntilMS == nil || *pool.ValidUntilMS != lendingContractNowMS+int64(2.2727*float64(lendingContractDayMS)) {
		t.Fatalf("valid_until = %v, want the earliest refill", pool.ValidUntilMS)
	}
	if len(pool.OutsideCards) != 1 {
		t.Fatalf("outside cards = %+v", pool.OutsideCards)
	}
	outside := pool.OutsideCards[0]
	if outside.AuthIndex != "xai-a" || outside.Provider != "xai" || outside.PoolID != "" ||
		outside.DisplayName != "research@acme-labs.example" || outside.Priority == nil || *outside.Priority != 4 {
		t.Fatalf("outside card = %+v", outside)
	}
	if overview.Lending.UnpooledCount != 1 || len(overview.Lending.Unpooled) != 1 {
		t.Fatalf("unpooled = %+v", overview.Lending.Unpooled)
	}
	unpooled := overview.Lending.Unpooled[0]
	if unpooled.AuthIndex != "xai-a" || len(unpooled.ReasonCodes) != 1 || unpooled.ReasonCodes[0] != quota.LendingReasonPlanTypeUnknown {
		t.Fatalf("unpooled = %+v", unpooled)
	}

	var detail struct {
		Quota   QuotaItemDTO     `json:"quota"`
		Lending lendingDetailDTO `json:"lending"`
	}
	getLendingContractJSON(t, contract.client, contract.baseURL+"/omc/api/v1/management/quota/codex-b", &detail)
	if detail.Lending.Pool == nil {
		t.Fatal("detail for a pooled credential has no pool")
	}
	if len(detail.Lending.UnpooledReasonCodes) != 0 {
		t.Fatalf("pooled detail unpooled reasons = %v", detail.Lending.UnpooledReasonCodes)
	}
	if detail.Lending.GeneratedAtMS != overview.Lending.GeneratedAtMS {
		t.Fatalf("detail generated_at_ms = %d, overview = %d", detail.Lending.GeneratedAtMS, overview.Lending.GeneratedAtMS)
	}
	if !reflect.DeepEqual(*detail.Lending.Pool, pool) {
		t.Fatalf("detail pool differs from overview pool:\ndetail   = %+v\noverview = %+v", *detail.Lending.Pool, pool)
	}

	var unpooledDetail struct {
		Lending lendingDetailDTO `json:"lending"`
	}
	getLendingContractJSON(t, contract.client, contract.baseURL+"/omc/api/v1/management/quota/xai-a", &unpooledDetail)
	if unpooledDetail.Lending.Pool != nil {
		t.Fatalf("unpooled detail returned a pool: %+v", unpooledDetail.Lending.Pool)
	}
	if len(unpooledDetail.Lending.UnpooledReasonCodes) != 1 || unpooledDetail.Lending.UnpooledReasonCodes[0] != quota.LendingReasonPlanTypeUnknown {
		t.Fatalf("unpooled detail reasons = %v", unpooledDetail.Lending.UnpooledReasonCodes)
	}

	for _, forbidden := range []string{`"target_url"`, `"raw_content"`, `"api_key"`, `"management_key"`, `"apply"`} {
		if strings.Contains(string(overviewBody), forbidden) {
			t.Fatalf("overview leaked %s: %s", forbidden, overviewBody)
		}
	}
}

func TestQuotaLendingDTOAllowlist(t *testing.T) {
	contract, cleanup := startLendingContractServer(t)
	defer cleanup()

	response, payload := getJSON(t, contract.client, contract.baseURL+"/omc/api/v1/management/quota")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", response.StatusCode, payload)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	lending, ok := decoded["lending"].(map[string]any)
	if !ok {
		t.Fatalf("lending is not an object: %s", payload)
	}
	assertLendingKeys(t, lending, []string{"generated_at_ms", "pools", "unpooled_count", "unpooled"})
	pools := lending["pools"].([]any)
	if len(pools) != 1 {
		t.Fatalf("pools = %+v", pools)
	}
	pool := pools[0].(map[string]any)
	assertLendingKeys(t, pool, []string{"id", "provider", "plan_type", "plan_label", "window_kind", "state", "valid_until_ms", "observed_at_ms", "fresh_until_ms", "is_reference", "is_balanced", "needs_change_count", "hold_count", "reason_codes", "cards", "outside_cards"})
	cards := pool["cards"].([]any)
	if len(cards) != 2 {
		t.Fatalf("cards = %+v", cards)
	}
	assertLendingKeys(t, cards[0].(map[string]any), []string{"auth_index", "name", "display_name", "role", "tier", "window_id", "window_kind", "remaining_percent", "reset_at_ms", "reset_accuracy", "observed_at_ms", "fresh_until_ms", "days_to_reset", "daily_available_percent", "is_comparable", "priority", "priority_known", "priority_is_default", "can_edit_priority", "suggested_priority", "needs_change", "primary_reason", "reason_codes"})
	outside := pool["outside_cards"].([]any)
	if len(outside) != 1 {
		t.Fatalf("outside cards = %+v", outside)
	}
	assertLendingKeys(t, outside[0].(map[string]any), []string{"auth_index", "name", "display_name", "provider", "pool_id", "pool_label", "priority", "priority_known", "priority_is_default"})
	unpooled := lending["unpooled"].([]any)
	if len(unpooled) != 1 {
		t.Fatalf("unpooled = %+v", unpooled)
	}
	assertLendingKeys(t, unpooled[0].(map[string]any), []string{"auth_index", "name", "reason_codes"})
}

func assertLendingKeys(t *testing.T, object map[string]any, allowed []string) {
	t.Helper()
	permitted := make(map[string]bool, len(allowed))
	for _, key := range allowed {
		permitted[key] = true
	}
	for key := range object {
		if !permitted[key] {
			t.Fatalf("lending object carries %q outside the allowlist: %+v", key, object)
		}
	}
}

// TestQuotaLendingPreservesResetAdvice pins the existing projection: the overview's
// reset advice is exactly what consoleResetAdvice returns for the same roster, so
// adding the lending object cannot quietly re-shape or move it.
func TestQuotaLendingPreservesResetAdvice(t *testing.T) {
	contract, cleanup := startLendingContractServer(t)
	defer cleanup()

	var overview struct {
		Quotas []QuotaItemDTO `json:"quotas"`
	}
	getLendingContractJSON(t, contract.client, contract.baseURL+"/omc/api/v1/management/quota", &overview)

	advice, err := contract.handler.consoleResetAdvice(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range overview.Quotas {
		if !reflect.DeepEqual(item.ResetAdvice, advice[item.AuthIndex]) {
			t.Fatalf("reset advice for %s changed: %+v vs %+v", item.AuthIndex, item.ResetAdvice, advice[item.AuthIndex])
		}
	}
}

// seedLendingContractSnapshotWithoutReset replaces a seat's newest reading with one
// whose reset is only a label. That seat holds, and the only remaining participant
// cannot form a board.
func seedLendingContractSnapshotWithoutReset(t *testing.T, repo *repository.Repository, authIndex string, remaining float64) {
	t.Helper()
	used := 100 - remaining
	period := 168.0
	windows := []quota.QuotaWindow{{
		ID:               "weekly",
		Label:            "Weekly",
		Kind:             "weekly",
		Scope:            "standard",
		UsedPercent:      &used,
		RemainingPercent: &remaining,
		PeriodHours:      &period,
		ResetAccuracy:    "text",
		ResetLabel:       "in a few days",
	}}
	windowsJSON, err := json.Marshal(windows)
	if err != nil {
		t.Fatal(err)
	}
	plan := quota.QuotaPlan{PlanType: "pro", PlanLabel: "Pro 200", Tier: "elite"}
	planJSON, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveQuotaSnapshot(context.Background(), repository.QuotaSnapshotRecord{
		ID:           "snap-" + authIndex + "-unavailable",
		AuthIndex:    authIndex,
		Provider:     "codex",
		Status:       "healthy",
		PlanType:     plan.PlanType,
		PlanTier:     plan.Tier,
		PlanJSON:     string(planJSON),
		WindowsJSON:  string(windowsJSON),
		ObservedAtMS: lendingContractNowMS,
		CreatedAtMS:  lendingContractNowMS,
	}); err != nil {
		t.Fatal(err)
	}
}

// TestQuotaLendingNoAdvicePoolOmitsValidUntil pins the contract's empty
// valid_until_ms for a pool whose cards cannot form a board.
func TestQuotaLendingNoAdvicePoolOmitsValidUntil(t *testing.T) {
	contract, cleanup := startLendingContractServer(t)
	defer cleanup()

	seedLendingContractSnapshotWithoutReset(t, contract.repo, "codex-a", 70)

	var overview struct {
		Lending lendingOverviewDTO `json:"lending"`
	}
	payload := getLendingContractJSON(t, contract.client, contract.baseURL+"/omc/api/v1/management/quota", &overview)
	if len(overview.Lending.Pools) != 1 || overview.Lending.Pools[0].State != quota.LendingStateNoAdvice {
		t.Fatalf("pools = %+v", overview.Lending.Pools)
	}
	pool := overview.Lending.Pools[0]
	if pool.ValidUntilMS != nil {
		t.Fatalf("no-advice pool DTO carried valid_until_ms = %d", *pool.ValidUntilMS)
	}
	if pool.HoldCount != 2 || pool.NeedsChangeCount != 0 {
		t.Fatalf("pool counts = %+v", pool)
	}
	if len(pool.ReasonCodes) != 2 || pool.ReasonCodes[0] != quota.LendingReasonResetTimeUnknown || pool.ReasonCodes[1] != quota.LendingReasonWindowTypeMismatch {
		t.Fatalf("pool reasons = %v", pool.ReasonCodes)
	}

	var decoded struct {
		Lending struct {
			Pools []map[string]json.RawMessage `json:"pools"`
		} `json:"lending"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Lending.Pools) != 1 {
		t.Fatalf("pools = %+v", decoded.Lending.Pools)
	}
	if value, ok := decoded.Lending.Pools[0]["valid_until_ms"]; ok && string(value) != "null" {
		t.Fatalf("no-advice pool serialized valid_until_ms = %s", value)
	}
}

// TestQuotaLendingDetailDegradesWhenReadsFail pins the baseline detail contract: a
// failed snapshot or cooldown read still serves the credential's original fields with
// 200, and lending is omitted rather than projected from incomplete data.
func TestQuotaLendingDetailDegradesWhenReadsFail(t *testing.T) {
	for _, table := range []string{"quota_snapshots", "error_events"} {
		t.Run(table, func(t *testing.T) {
			contract, cleanup := startLendingContractServer(t)
			defer cleanup()

			if _, err := contract.repo.SQL().Exec("DROP TABLE " + table); err != nil {
				t.Fatalf("drop %s: %v", table, err)
			}

			response, payload := getJSON(t, contract.client, contract.baseURL+"/omc/api/v1/management/quota/codex-b")
			if response.StatusCode != http.StatusOK {
				t.Fatalf("status = %d: %s", response.StatusCode, payload)
			}
			var detail struct {
				Quota   QuotaItemDTO      `json:"quota"`
				Lending *lendingDetailDTO `json:"lending"`
			}
			if err := json.Unmarshal(payload, &detail); err != nil {
				t.Fatal(err)
			}
			if detail.Quota.AuthIndex != "codex-b" || detail.Quota.Name != "codex-b.json" {
				t.Fatalf("original quota fields = %+v", detail.Quota.NormalizedQuota)
			}
			if detail.Lending != nil {
				t.Fatalf("lending = %+v, want null", detail.Lending)
			}
		})
	}
}

func TestLendingCardInputsReadsRosterPriorityAndEditable(t *testing.T) {
	items := []QuotaItemDTO{
		{NormalizedQuota: quota.NormalizedQuota{AuthIndex: "codex-a", Name: "codex-a.json", Provider: "codex", Plan: &quota.QuotaPlan{PlanType: "pro"}}},
		{NormalizedQuota: quota.NormalizedQuota{AuthIndex: "codex-dup", Name: "codex-a.json", Provider: "codex", Plan: &quota.QuotaPlan{PlanType: "pro"}}},
		{NormalizedQuota: quota.NormalizedQuota{AuthIndex: "codex-unique", Name: "codex-u.json", Provider: "codex", Plan: &quota.QuotaPlan{PlanType: "pro"}}},
		{NormalizedQuota: quota.NormalizedQuota{AuthIndex: "virtual", Name: "virtual.json", Provider: "codex", Plan: &quota.QuotaPlan{PlanType: "pro"}}},
		{NormalizedQuota: quota.NormalizedQuota{AuthIndex: "compat", Name: "opencode", Provider: "opencode"}},
	}
	files := []management.AuthFile{
		{AuthIndex: "codex-a", Name: "codex-a.json", Type: "codex", Email: "ops@example.test", Priority: 15, Models: []management.AuthModel{{ID: "gpt-5"}}},
		{AuthIndex: "codex-dup", Name: "codex-a.json", Type: "codex", Priority: 3},
		{AuthIndex: "codex-unique", Name: "codex-u.json", Type: "codex", Priority: 2},
		{AuthIndex: "virtual", Name: "virtual.json", Type: "codex", RuntimeOnly: true, Priority: 9},
		{AuthIndex: "compat", Name: "opencode", Type: "openai-compatibility", Priority: 0},
	}
	inputs := lendingCardInputs(items, files)
	byIndex := map[string]quota.LendingCardInput{}
	for _, input := range inputs {
		byIndex[input.AuthIndex] = input
	}

	if input := byIndex["codex-a"]; !input.PriorityKnown || input.Priority != 15 || input.CanEditPriority || input.Email != "ops@example.test" || len(input.Models) != 1 || input.Models[0] != "gpt-5" {
		t.Fatalf("codex-a input = %+v, want priority known and not editable", input)
	}
	if input := byIndex["codex-dup"]; !input.PriorityKnown || input.CanEditPriority {
		t.Fatalf("duplicate name input = %+v, want priority known and not editable", input)
	}
	if input := byIndex["codex-unique"]; !input.PriorityKnown || input.Priority != 2 || !input.CanEditPriority {
		t.Fatalf("unique input = %+v, want priority known and editable", input)
	}
	if input := byIndex["virtual"]; !input.PriorityKnown || input.CanEditPriority {
		t.Fatalf("runtime-only input = %+v, want priority known and not editable", input)
	}
	if input := byIndex["compat"]; input.PriorityKnown || input.CanEditPriority {
		t.Fatalf("compatibility input = %+v, want priority unknown and not editable", input)
	}
}

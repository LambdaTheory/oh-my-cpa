package operations

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oh-my-cpa/oh-my-cpa/internal/capability"
	"github.com/oh-my-cpa/oh-my-cpa/internal/cpa/gateway"
	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

// The group fixture keeps advice evidence deliberately small: one healthy, one
// nearly spent, one unmeasured and one exhausted credential. Every route stands
// for one client-requested name, so each member resolves to a single upstream.
func modelGroupsFixture(t *testing.T) (*Service, context.Context, int64) {
	t.Helper()
	ctx := context.Background()
	database, err := repository.Open(ctx, filepath.Join(t.TempDir(), "model-groups.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	repo := repository.New(database)
	nowMS := int64(2_000_000_000_000)

	directory := ModelSquareDirectory{Models: []gateway.Model{}, Providers: []ModelSquareProvider{{ID: "provider-1", Family: "claude"}}}
	credentials := []AdviceCredential{}
	quotas := []Quota{}
	for _, model := range []string{"model-ok", "model-tight", "model-unknown", "model-avoid"} {
		directory.Models = append(directory.Models, gateway.Model{ID: model})
		directory.Routes = append(directory.Routes, ModelSquareRoute{ProviderID: "provider-1", UpstreamModel: "upstream-" + model, CallPoint: model})
		credentials = append(credentials, AdviceCredential{AuthIndex: "auth-" + model, Provider: "claude", ProviderID: "provider-1", Models: []string{"upstream-" + model}})
	}
	window := func(used float64) quota.QuotaWindow {
		return quota.QuotaWindow{Label: "Weekly", Kind: "weekly", Scope: "standard", UsedPercent: advicePercent(used), RemainingPercent: advicePercent(100 - used)}
	}
	quotas = append(quotas,
		Quota{AuthIndex: "auth-model-ok", Provider: "claude", Status: "healthy", ObservedAtMS: nowMS - 1000, Windows: []quota.QuotaWindow{window(10)}},
		Quota{AuthIndex: "auth-model-tight", Provider: "claude", Status: "warning", ObservedAtMS: nowMS - 1000, Windows: []quota.QuotaWindow{window(85)}},
		// auth-model-unknown deliberately has no reading: the member must still
		// come back, marked unknown, rather than disappear.
		Quota{AuthIndex: "auth-model-avoid", Provider: "claude", Status: "exhausted", ObservedAtMS: nowMS - 1000, Windows: []quota.QuotaWindow{window(100)}},
	)
	service := &Service{Repo: repo,
		AdviceDirectory:   func(context.Context) (ModelSquareDirectory, error) { return directory, nil },
		AdviceCredentials: func(context.Context) ([]AdviceCredential, error) { return credentials, nil },
		Quotas:            func(context.Context) ([]Quota, error) { return quotas, nil },
	}
	return service, capability.WithAnchor(ctx, nowMS), nowMS
}

// groupWithMembers reuses a seeded preset when the test names one, so the
// fixture exercises the same groups a deployment starts with.
func groupWithMembers(t *testing.T, service *Service, ctx context.Context, nowMS int64, name string, models ...string) repository.ModelGroup {
	t.Helper()
	group, err := service.Repo.GetModelGroupByName(ctx, name)
	if err != nil {
		group, err = service.Repo.CreateModelGroup(ctx, name, nowMS, repository.AuditEvent{Action: "test"})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(models) > 0 {
		if _, err := service.Repo.AddModelGroupMembers(ctx, group.ID, models, nowMS, repository.AuditEvent{Action: "test"}); err != nil {
			t.Fatal(err)
		}
		if group, err = service.Repo.GetModelGroup(ctx, group.ID); err != nil {
			t.Fatal(err)
		}
	}
	return group
}

func invokeModelGroupCapability(t *testing.T, service *Service, ctx context.Context, name, arguments string) capability.Result {
	t.Helper()
	registry := capability.NewRegistry()
	if err := service.registerModelGroups(registry); err != nil {
		t.Fatal(err)
	}
	executor := &capability.Executor{Registry: registry, Store: repository.AgentStore{Repo: service.Repo}}
	result, err := executor.Invoke(ctx, capability.Principal{ID: "administrator", Adapter: "mcp", IsAdmin: true}, name, json.RawMessage(arguments), "")
	if err != nil {
		t.Fatalf("invoke %s: %v", name, err)
	}
	return result
}

func TestModelGroupsListReturnsNamesAndMemberCounts(t *testing.T) {
	service, ctx, nowMS := modelGroupsFixture(t)
	groupWithMembers(t, service, ctx, nowMS, "力工组", "model-ok", "model-tight")

	result := invokeModelGroupCapability(t, service, ctx, "model_groups_list", `{}`)
	if result.Status != "success" {
		t.Fatalf("result = %+v", result)
	}
	var output ModelGroupsListResult
	if err := json.Unmarshal(result.Data, &output); err != nil {
		t.Fatal(err)
	}
	byName := map[string]int{}
	for _, group := range output.Groups {
		byName[group.Name] = group.MemberCount
	}
	if byName["力工组"] != 2 {
		t.Fatalf("力工组 member count = %d, groups = %+v", byName["力工组"], output.Groups)
	}
	for _, preset := range repository.PresetModelGroupNames() {
		if _, listed := byName[preset]; !listed {
			t.Errorf("preset %q is missing from %+v", preset, output.Groups)
		}
	}
}

func TestModelGroupsModelsSortsByAvailabilityThenGroupOrder(t *testing.T) {
	service, ctx, nowMS := modelGroupsFixture(t)
	// The operator's order is deliberately not the availability order.
	groupWithMembers(t, service, ctx, nowMS, "力工组", "model-avoid", "model-ok", "model-unknown", "model-tight")

	result := invokeModelGroupCapability(t, service, ctx, "model_groups_models", `{"group":"力工组"}`)
	if result.Status != "success" {
		t.Fatalf("result = %+v", result)
	}
	var output ModelGroupsModelsResult
	if err := json.Unmarshal(result.Data, &output); err != nil {
		t.Fatal(err)
	}
	if !output.IsFound || output.Reason != "" {
		t.Fatalf("result = %+v", output)
	}
	wantModels := []string{"model-ok", "model-tight", "model-unknown", "model-avoid"}
	wantAvailability := []string{
		ModelGroupAvailabilityAvailable, ModelGroupAvailabilityTight,
		ModelGroupAvailabilityUnknown, ModelGroupAvailabilityUnavailable,
	}
	wantPositions := []int{2, 4, 3, 1}
	if len(output.Models) != len(wantModels) {
		t.Fatalf("models = %+v", output.Models)
	}
	for index, item := range output.Models {
		if item.Model != wantModels[index] || item.Availability != wantAvailability[index] || item.Position != wantPositions[index] {
			t.Errorf("models[%d] = %+v, want %s/%s/position %d", index, item, wantModels[index], wantAvailability[index], wantPositions[index])
		}
		if item.IsPreferred != (item.Position == 1) {
			t.Errorf("%s preferred = %v at position %d", item.Model, item.IsPreferred, item.Position)
		}
	}
	if !output.Models[3].IsPreferred {
		t.Error("the operator's first member must stay marked preferred even when it sorts last")
	}
}

func TestModelGroupsModelsMatchesModelAdviceVerdicts(t *testing.T) {
	service, ctx, nowMS := modelGroupsFixture(t)
	group := groupWithMembers(t, service, ctx, nowMS, "力工组", "model-ok", "model-tight", "model-unknown", "model-avoid")
	names := make([]string, 0, len(group.Members))
	for _, member := range group.Members {
		names = append(names, member.Model)
	}
	directory, credentials, quotas := service.modelGroupAdviceInputs(ctx)
	advice := ProjectModelAdvice(ModelAdviceInput{Models: names}, directory, quotas, credentials, nowMS, nil)
	verdicts := map[string]string{}
	for _, item := range advice.Models {
		verdicts[item.Model] = item.Verdict
	}
	want := map[string]string{"model-ok": "ok", "model-tight": "caution", "model-unknown": "unknown", "model-avoid": "avoid"}
	for model, verdict := range want {
		if verdicts[model] != verdict {
			t.Fatalf("%s advice verdict = %q, want %q (fixture is wrong)", model, verdicts[model], verdict)
		}
	}
	projected := projectModelGroupModels(group.Members, advice, nil)
	for _, item := range projected {
		if item.Availability != modelGroupAvailability(verdicts[item.Model]) {
			t.Errorf("%s availability = %q, advice %q", item.Model, item.Availability, verdicts[item.Model])
		}
	}
}

func TestModelGroupsModelsReturnsUnusableMembersAndEmptyGroups(t *testing.T) {
	service, ctx, nowMS := modelGroupsFixture(t)
	groupWithMembers(t, service, ctx, nowMS, "力工组", "model-avoid")

	result := invokeModelGroupCapability(t, service, ctx, "model_groups_models", `{"group":"力工组"}`)
	if result.Status != "success" {
		t.Fatalf("result = %+v", result)
	}
	var output ModelGroupsModelsResult
	if err := json.Unmarshal(result.Data, &output); err != nil {
		t.Fatal(err)
	}
	if len(output.Models) != 1 || output.Models[0].Model != "model-avoid" || output.Models[0].Availability != ModelGroupAvailabilityUnavailable {
		t.Fatalf("a spent model must still be returned: %+v", output.Models)
	}

	empty := invokeModelGroupCapability(t, service, ctx, "model_groups_models", `{"group":"前端组"}`)
	if empty.Status != "success" {
		t.Fatalf("empty group result = %+v", empty)
	}
	if err := json.Unmarshal(empty.Data, &output); err != nil {
		t.Fatal(err)
	}
	if !output.IsFound || output.Reason != modelGroupEmptyReason || len(output.Models) != 0 {
		t.Fatalf("empty group = %+v", output)
	}

	missing := invokeModelGroupCapability(t, service, ctx, "model_groups_models", `{"group":"没有这个组"}`)
	if missing.Status != "success" {
		t.Fatalf("missing group must be an explicit result, not an error: %+v", missing)
	}
	if err := json.Unmarshal(missing.Data, &output); err != nil {
		t.Fatal(err)
	}
	if output.IsFound || output.Reason != modelGroupNotFoundReason || len(output.Models) != 0 {
		t.Fatalf("missing group = %+v", output)
	}
}

func TestModelGroupsModelsReadsEveryMemberWhenQuotaIsUnavailable(t *testing.T) {
	service, ctx, nowMS := modelGroupsFixture(t)
	service.Quotas = func(context.Context) ([]Quota, error) { return nil, fmt.Errorf("quota store unavailable") }
	groupWithMembers(t, service, ctx, nowMS, "力工组", "model-ok", "model-avoid")

	result := invokeModelGroupCapability(t, service, ctx, "model_groups_models", `{"group":"力工组"}`)
	if result.Status != "success" {
		t.Fatalf("result = %+v", result)
	}
	var output ModelGroupsModelsResult
	if err := json.Unmarshal(result.Data, &output); err != nil {
		t.Fatal(err)
	}
	if len(output.Models) != 2 {
		t.Fatalf("models = %+v", output.Models)
	}
	for _, item := range output.Models {
		if item.Availability != ModelGroupAvailabilityUnknown {
			t.Errorf("%s availability = %q, want unknown without readings", item.Model, item.Availability)
		}
		if item.AverageUsagePercent != nil {
			t.Errorf("%s average usage = %v, want null without readings", item.Model, *item.AverageUsagePercent)
		}
	}
}

func TestModelGroupsModelsRenamedGroupIsUnknownUnderItsOldName(t *testing.T) {
	service, ctx, nowMS := modelGroupsFixture(t)
	group := groupWithMembers(t, service, ctx, nowMS, "Engineering")
	if _, err := service.Repo.RenameModelGroup(ctx, group.ID, "Platform", nowMS, repository.AuditEvent{Action: "test"}); err != nil {
		t.Fatal(err)
	}
	found := invokeModelGroupCapability(t, service, ctx, "model_groups_models", `{"group":"platform"}`)
	var output ModelGroupsModelsResult
	if err := json.Unmarshal(found.Data, &output); err != nil {
		t.Fatal(err)
	}
	if !output.IsFound || output.Group != "Platform" {
		t.Fatalf("a case-insensitive spelling must reach the renamed group: %+v", output)
	}
	old := invokeModelGroupCapability(t, service, ctx, "model_groups_models", `{"group":"Engineering"}`)
	if err := json.Unmarshal(old.Data, &output); err != nil {
		t.Fatal(err)
	}
	if output.IsFound || output.Reason != modelGroupNotFoundReason {
		t.Fatalf("the old name must be unknown: %+v", output)
	}
}

func insertGroupObservation(t *testing.T, repo *repository.Repository, authIndex string, usedPercent float64, observedAtMS int64, index int) {
	t.Helper()
	if _, err := repo.SQL().Exec(`INSERT INTO quota_observations (
		auth_index, provider, window_kind, scope, model, window_minutes, used_percent,
		reset_at_ms, status, observed_at_ms, event_key, created_at_ms, source)
		VALUES (?, 'claude', 'weekly', 'standard', '', 10080, ?, NULL, '', ?, ?, 0, 'passive')`,
		authIndex, usedPercent, observedAtMS, fmt.Sprintf("%s-%d", authIndex, index)); err != nil {
		t.Fatal(err)
	}
}

func TestModelGroupsModelsAveragesUsageAcrossAccounts(t *testing.T) {
	service, ctx, nowMS := modelGroupsFixture(t)
	group := groupWithMembers(t, service, ctx, nowMS, "力工组", "model-ok")

	// Two accounts serve the same model and window. One consumes 2 points a day,
	// the other 3. The PRD merges accounts first, so the member's daily usage is
	// 5, not the mean of 2 and 3.
	for index := 0; index <= 7; index++ {
		atMS := nowMS - int64(modelGroupsUsageDays) + int64(index)*int64(24*60*60*1000)
		insertGroupObservation(t, service.Repo, "auth-model-ok", float64(index)*2, atMS, index)
		insertGroupObservation(t, service.Repo, "auth-model-ok-2", float64(index)*3, atMS, index+100)
	}
	directory, credentials, quotas := service.modelGroupAdviceInputs(ctx)
	credentials = append(credentials, AdviceCredential{AuthIndex: "auth-model-ok-2", Provider: "claude", ProviderID: "provider-1", Models: []string{"upstream-model-ok"}})
	quotas = append(quotas, Quota{AuthIndex: "auth-model-ok-2", Provider: "claude", Status: "healthy", ObservedAtMS: nowMS - 1000,
		Windows: []quota.QuotaWindow{{Label: "Weekly", Kind: "weekly", Scope: "standard", UsedPercent: advicePercent(20), RemainingPercent: advicePercent(80)}}})
	usage := service.modelGroupAverageUsage(ctx, []string{"model-ok"}, directory, credentials, quotas, nowMS)
	if usage["model-ok"] == nil {
		t.Fatalf("model-ok has no average usage: %+v", usage)
	}
	if got := *usage["model-ok"]; got != 5 {
		t.Fatalf("average usage = %v, want 5 (2 + 3 consumed points a day)", got)
	}
	if len(group.Members) != 1 {
		t.Fatalf("group = %+v", group)
	}
}

func TestCombinedGroupDailyUsageHandlesResetsAndSparseHistory(t *testing.T) {
	bucket := func(observedAtMS int64, min, last float64, reset bool) repository.QuotaObservationBucket {
		return repository.QuotaObservationBucket{StartMS: observedAtMS, LastObservedAtMS: observedAtMS, MinUsedPercent: min, LastUsedPercent: last, ResetDetected: reset}
	}
	day := int64(24 * 60 * 60 * 1000)
	start := int64(1_700_000_000_000)

	// 80 points over the first day, a reset, then 15 points in the new cycle.
	// The first reading inside the span has no predecessor, so the spans are
	// 80 and 15 over two covered days: 95 / 2.
	series := modelGroupUsageSeries{periodMS: 7 * day, buckets: []repository.QuotaObservationBucket{
		bucket(start, 0, 0, false),
		bucket(start+day, 0, 80, false),
		bucket(start+2*day, 5, 15, true),
	}}
	rate, ok := combinedGroupDailyUsage([]modelGroupUsageSeries{series})
	if !ok {
		t.Fatal("reset history produced no rate")
	}
	if rate != 47.5 {
		t.Fatalf("rate = %v, want 47.5 over two covered days", rate)
	}

	// A single afternoon cannot become a daily rate.
	sparse := modelGroupUsageSeries{periodMS: 7 * day, buckets: []repository.QuotaObservationBucket{
		bucket(start, 0, 5, false),
		bucket(start+60_000, 5, 10, false),
	}}
	if _, ok := combinedGroupDailyUsage([]modelGroupUsageSeries{sparse}); ok {
		t.Fatal("coverage under a day must not produce a daily rate")
	}
}

func TestModelGroupCapabilitiesAreReadOnly(t *testing.T) {
	registry := capability.NewRegistry()
	if err := (&Service{}).registerModelGroups(registry); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"model_groups_list", "model_groups_models"} {
		definition, err := registry.Lookup(name, capability.Principal{Adapter: "mcp", IsAdmin: true})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if definition.Metadata.Permission != "read" || definition.Metadata.Risk != "low" {
			t.Errorf("%s declaration = %+v", name, definition.Metadata)
		}
		adapters := map[string]bool{}
		for _, adapter := range definition.Metadata.Adapters {
			adapters[adapter] = true
		}
		if !adapters["agent"] || !adapters["mcp"] {
			t.Errorf("%s adapters = %v", name, definition.Metadata.Adapters)
		}
		if definition.Prepare != nil {
			t.Errorf("%s is a read and must not prepare an operation", name)
		}
	}
	schema, err := json.Marshal(registryLookup(t, registry, "model_groups_models").InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	var inputSchema struct {
		Required   []string `json:"required"`
		Properties map[string]struct {
			Type any `json:"type"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(schema, &inputSchema); err != nil {
		t.Fatal(err)
	}
	if len(inputSchema.Required) != 1 || inputSchema.Required[0] != "group" || inputSchema.Properties["group"].Type != "string" {
		t.Fatalf("input schema = %s", schema)
	}
}

func registryLookup(t *testing.T, registry *capability.Registry, name string) *capability.Definition {
	t.Helper()
	definition, err := registry.Lookup(name, capability.Principal{Adapter: "mcp", IsAdmin: true})
	if err != nil {
		t.Fatal(err)
	}
	return definition
}

// largeModelGroupService builds a group whose members are all connected, so an
// availability a test cannot explain is a projection bug rather than missing
// evidence. Healthy members are healthy; the rest are exhausted.
func largeModelGroupService(t *testing.T, healthy, exhausted, nameLength int) (*Service, context.Context, int64, []string) {
	t.Helper()
	ctx := context.Background()
	database, err := repository.Open(ctx, filepath.Join(t.TempDir(), "large-model-groups.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	nowMS := int64(2_000_000_000_000)
	directory := ModelSquareDirectory{Models: []gateway.Model{}, Providers: []ModelSquareProvider{{ID: "provider-1", Family: "claude"}}}
	credentials := []AdviceCredential{}
	quotas := []Quota{}
	names := []string{}
	for index := 0; index < healthy+exhausted; index++ {
		name := fmt.Sprintf("model-%03d-%s", index, strings.Repeat("x", nameLength))
		names = append(names, name)
		directory.Models = append(directory.Models, gateway.Model{ID: name})
		directory.Routes = append(directory.Routes, ModelSquareRoute{ProviderID: "provider-1", UpstreamModel: "upstream-" + name, CallPoint: name})
		credentials = append(credentials, AdviceCredential{AuthIndex: fmt.Sprintf("auth-%03d", index), Provider: "claude", ProviderID: "provider-1", Models: []string{"upstream-" + name}})
		used, status := 10.0, "healthy"
		if index >= healthy {
			used, status = 100, "exhausted"
		}
		quotas = append(quotas, Quota{AuthIndex: fmt.Sprintf("auth-%03d", index), Provider: "claude", Status: status, ObservedAtMS: nowMS - 1000,
			Windows: []quota.QuotaWindow{{Label: "Weekly", Kind: "weekly", Scope: "standard", UsedPercent: advicePercent(used), RemainingPercent: advicePercent(100 - used)}}})
	}
	service := &Service{Repo: repository.New(database),
		AdviceDirectory:   func(context.Context) (ModelSquareDirectory, error) { return directory, nil },
		AdviceCredentials: func(context.Context) ([]AdviceCredential, error) { return credentials, nil },
		Quotas:            func(context.Context) ([]Quota, error) { return quotas, nil },
	}
	return service, capability.WithAnchor(ctx, nowMS), nowMS, names
}

// ProjectModelAdvice bounds its own result to model_advice's 50-row budget.
// A group of more than 50 members must not inherit that truncation: every
// member keeps the verdict the advice projection gives it on its own.
func TestModelGroupsModelsBeyondAdviceLimitKeepsVerdicts(t *testing.T) {
	service, ctx, nowMS, names := largeModelGroupService(t, 60, 0, 0)
	groupWithMembers(t, service, ctx, nowMS, "Big group", names...)

	result := invokeModelGroupCapability(t, service, ctx, "model_groups_models", `{"group":"Big group"}`)
	if result.Status != "success" {
		t.Fatalf("result = %+v", result)
	}
	var output ModelGroupsModelsResult
	if err := json.Unmarshal(result.Data, &output); err != nil {
		t.Fatal(err)
	}
	if len(output.Models) != 60 || output.Omitted != 0 {
		t.Fatalf("models = %d, omitted = %d", len(output.Models), output.Omitted)
	}
	for _, item := range output.Models {
		if item.Availability != ModelGroupAvailabilityAvailable {
			t.Fatalf("%s availability = %q, want available for every connected member", item.Model, item.Availability)
		}
	}
}

// The byte budget trims rows, not verdicts: every row that survives the budget
// must still carry the verdict advice gives it, and an exhausted member must
// never become "unknown" and sort ahead of an unavailable one.
func TestModelGroupsModelsByteBudgetKeepsVerdicts(t *testing.T) {
	service, ctx, nowMS, names := largeModelGroupService(t, 60, 40, 240)
	groupWithMembers(t, service, ctx, nowMS, "Byte budget group", names...)

	result := invokeModelGroupCapability(t, service, ctx, "model_groups_models", `{"group":"Byte budget group"}`)
	if result.Status != "success" {
		t.Fatalf("result = %+v", result)
	}
	var output ModelGroupsModelsResult
	if err := json.Unmarshal(result.Data, &output); err != nil {
		t.Fatal(err)
	}
	available, unavailable := 0, 0
	for _, item := range output.Models {
		switch item.Availability {
		case ModelGroupAvailabilityAvailable:
			available++
		case ModelGroupAvailabilityUnavailable:
			unavailable++
		default:
			t.Fatalf("%s availability = %q, want the advice verdict", item.Model, item.Availability)
		}
	}
	if available != 60 {
		t.Fatalf("available = %d, want all 60 healthy members", available)
	}
	if unavailable == 0 {
		t.Fatal("an exhausted member must be returned as unavailable, not trimmed behind a wrong verdict")
	}
	if len(output.Models)+output.Omitted != 100 || output.Omitted == 0 {
		t.Fatalf("models = %d, omitted = %d, want the byte budget to account for 100", len(output.Models), output.Omitted)
	}
}

func insertGroupObservationWithReset(t *testing.T, repo *repository.Repository, authIndex string, usedPercent float64, resetAtMS int64, observedAtMS int64, index int) {
	t.Helper()
	if _, err := repo.SQL().Exec(`INSERT INTO quota_observations (
		auth_index, provider, window_kind, scope, model, window_minutes, used_percent,
		reset_at_ms, status, observed_at_ms, event_key, created_at_ms, source)
		VALUES (?, 'claude', 'weekly', 'standard', '', 10080, ?, ?, '', ?, ?, 0, 'passive')`,
		authIndex, usedPercent, resetAtMS, observedAtMS, fmt.Sprintf("%s-reset-%d", authIndex, index)); err != nil {
		t.Fatal(err)
	}
}

// A model's average must be the consumption inside the interval every matching
// account covers, divided by that interval - not each account's own average
// added together. Account A consumes 4 points a day but only after day three;
// account B covers days three to seven at 1 point a day. Inside the common
// interval that is (4*4 + 1*4) / 4 = 5, while per-account daily means summed
// would give about 3.86.
func TestModelGroupsModelsAverageUsageUsesCommonInterval(t *testing.T) {
	service, ctx, nowMS := modelGroupsFixture(t)
	groupWithMembers(t, service, ctx, nowMS, "力工组", "model-ok")
	day := int64(24 * 60 * 60 * 1000)
	weekStart := nowMS - int64(modelGroupsUsageDays)
	first := []float64{0, 0, 0, 4, 8, 12, 16, 20}
	for index, used := range first {
		insertGroupObservation(t, service.Repo, "auth-model-ok", used, weekStart+int64(index)*day, index)
	}
	second := []float64{0, 1, 2, 3, 4}
	for index, used := range second {
		insertGroupObservation(t, service.Repo, "auth-model-ok-2", used, weekStart+int64(3+index)*day, index+10)
	}
	directory, credentials, quotas := service.modelGroupAdviceInputs(ctx)
	credentials = append(credentials, AdviceCredential{AuthIndex: "auth-model-ok-2", Provider: "claude", ProviderID: "provider-1", Models: []string{"upstream-model-ok"}})
	quotas = append(quotas, Quota{AuthIndex: "auth-model-ok-2", Provider: "claude", Status: "healthy", ObservedAtMS: nowMS - 1000,
		Windows: []quota.QuotaWindow{{Label: "Weekly", Kind: "weekly", Scope: "standard", UsedPercent: advicePercent(20), RemainingPercent: advicePercent(80)}}})
	usage := service.modelGroupAverageUsage(ctx, []string{"model-ok"}, directory, credentials, quotas, nowMS)
	if usage["model-ok"] == nil {
		t.Fatalf("model-ok has no average usage: %+v", usage)
	}
	if got := *usage["model-ok"]; got != 5 {
		t.Fatalf("average usage = %v, want 5 from the common four-day interval", got)
	}
}

// A matching account without usable history makes the model's average
// incomplete. Returning the other account's rate would present a partial figure
// as the whole model's number.
func TestModelGroupsModelsAverageUsageRequiresEveryAccount(t *testing.T) {
	service, ctx, nowMS := modelGroupsFixture(t)
	groupWithMembers(t, service, ctx, nowMS, "力工组", "model-ok")
	day := int64(24 * 60 * 60 * 1000)
	weekStart := nowMS - int64(modelGroupsUsageDays)
	for index := 0; index <= 7; index++ {
		insertGroupObservation(t, service.Repo, "auth-model-ok", float64(index)*2, weekStart+int64(index)*day, index)
	}
	// The second account serves the model but has never been observed.
	directory, credentials, quotas := service.modelGroupAdviceInputs(ctx)
	credentials = append(credentials, AdviceCredential{AuthIndex: "auth-model-ok-2", Provider: "claude", ProviderID: "provider-1", Models: []string{"upstream-model-ok"}})
	quotas = append(quotas, Quota{AuthIndex: "auth-model-ok-2", Provider: "claude", Status: "healthy", ObservedAtMS: nowMS - 1000,
		Windows: []quota.QuotaWindow{{Label: "Weekly", Kind: "weekly", Scope: "standard", UsedPercent: advicePercent(20), RemainingPercent: advicePercent(80)}}})
	usage := service.modelGroupAverageUsage(ctx, []string{"model-ok"}, directory, credentials, quotas, nowMS)
	if usage["model-ok"] != nil {
		t.Fatalf("average usage = %v, want null while a serving account has no history", *usage["model-ok"])
	}
}

// A new cycle can start while the percentage is already higher: 40% before a
// weekly boundary, 60% after it. The reset instant moved forward by a period,
// so the 60 is consumption in the new cycle, not a 20-point increase.
func TestModelGroupsModelsAverageUsageCountsForwardReset(t *testing.T) {
	service, ctx, nowMS := modelGroupsFixture(t)
	groupWithMembers(t, service, ctx, nowMS, "力工组", "model-ok")
	day := int64(24 * 60 * 60 * 1000)
	weekStart := nowMS - int64(modelGroupsUsageDays)
	oldReset := nowMS - 6*day
	newReset := oldReset + 7*day
	insertGroupObservationWithReset(t, service.Repo, "auth-model-ok", 40, oldReset, weekStart, 0)
	insertGroupObservationWithReset(t, service.Repo, "auth-model-ok", 60, newReset, weekStart+day, 1)
	directory := ModelSquareDirectory{Models: []gateway.Model{{ID: "model-ok"}}, Providers: []ModelSquareProvider{{ID: "provider-1", Family: "claude"}},
		Routes: []ModelSquareRoute{{ProviderID: "provider-1", UpstreamModel: "upstream-model-ok", CallPoint: "model-ok"}}}
	credentials := []AdviceCredential{{AuthIndex: "auth-model-ok", Provider: "claude", ProviderID: "provider-1", Models: []string{"upstream-model-ok"}}}
	period := 168.0
	quotas := []Quota{{AuthIndex: "auth-model-ok", Provider: "claude", Status: "healthy", ObservedAtMS: nowMS - 1000,
		Windows: []quota.QuotaWindow{{Label: "Weekly", Kind: "weekly", Scope: "standard", PeriodHours: &period, UsedPercent: advicePercent(60), RemainingPercent: advicePercent(40)}}}}
	usage := service.modelGroupAverageUsage(ctx, []string{"model-ok"}, directory, credentials, quotas, nowMS)
	if usage["model-ok"] == nil {
		t.Fatalf("model-ok has no average usage: %+v", usage)
	}
	if got := *usage["model-ok"]; got != 60 {
		t.Fatalf("average usage = %v, want 60 from the new cycle", got)
	}
}

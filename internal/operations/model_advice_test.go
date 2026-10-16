package operations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oh-my-cpa/oh-my-cpa/internal/capability"
	"github.com/oh-my-cpa/oh-my-cpa/internal/cpa/gateway"
	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

func advicePercent(value float64) *float64 { return &value }

func adviceFixture() (ModelSquareDirectory, []Quota, []AdviceCredential) {
	const nowMS = int64(2_000_000_000_000)
	models := []string{"healthy", "warning", "exhausted", "unread", "mixed", "scoped", "excluded"}
	directory := ModelSquareDirectory{Models: []gateway.Model{}, Routes: []ModelSquareRoute{}, Providers: []ModelSquareProvider{{ID: "oauth:claude", Family: "claude", IsOAuth: true}}}
	for _, model := range models {
		directory.Models = append(directory.Models, gateway.Model{ID: model})
		directory.Routes = append(directory.Routes, ModelSquareRoute{ProviderID: "oauth:claude", UpstreamModel: "claude-sonnet-4", CallPoint: model})
	}
	credentials := []AdviceCredential{}
	quotas := []Quota{}
	add := func(index, model, status string, windows ...quota.QuotaWindow) {
		credentials = append(credentials, AdviceCredential{AuthIndex: index, Provider: "claude", Models: []string{model}})
		quotas = append(quotas, Quota{AuthIndex: index, Provider: "claude", Status: status, ObservedAtMS: nowMS - 10_000, Windows: windows})
	}
	standard := func(used float64) quota.QuotaWindow {
		return quota.QuotaWindow{Label: "Weekly", Kind: "weekly", Scope: "standard", UsedPercent: advicePercent(used), RemainingPercent: advicePercent(100 - used)}
	}
	add("healthy", "healthy", "healthy", standard(10))
	add("warning", "warning", "warning", standard(85))
	add("exhausted", "exhausted", "exhausted", standard(100))
	add("unread", "unread", "idle")
	add("mixed-blocked", "mixed", "exhausted", standard(100))
	add("mixed-unread", "mixed", "unpublished")
	add("scoped", "scoped", "healthy",
		quota.QuotaWindow{Label: "Other", Scope: "model", Model: "claude-opus-4", RemainingPercent: advicePercent(0), UsedPercent: advicePercent(100)},
		quota.QuotaWindow{Label: "Sonnet", Kind: "weekly", Scope: "group", ModelFamilies: []string{"claude-sonnet"}, RemainingPercent: advicePercent(75), UsedPercent: advicePercent(25)})
	add("excluded", "excluded", "healthy", standard(5))
	credentials[len(credentials)-1].ExcludedModels = []string{"claude-sonnet-*"}
	return directory, quotas, credentials
}

func TestModelAdviceVerdictsScopesAndPrivacy(t *testing.T) {
	directory, quotas, credentials := adviceFixture()
	result := ProjectModelAdvice(ModelAdviceInput{}, directory, quotas, credentials, 2_000_000_000_000, nil)
	verdicts := map[string]string{}
	for _, item := range result.Models {
		verdicts[item.Model] = item.Verdict
	}
	for model, expected := range map[string]string{
		"healthy": "ok", "warning": "caution", "exhausted": "avoid", "unread": "unknown",
		"mixed": "unknown", "scoped": "ok", "excluded": "unknown",
	} {
		if verdicts[model] != expected {
			t.Errorf("%s: verdict %q, want %q", model, verdicts[model], expected)
		}
	}
	if result.Recommended == nil || *result.Recommended != "healthy" {
		t.Fatalf("recommended = %v", result.Recommended)
	}
	for index, item := range result.Models {
		if index < 4 && (item.Rank == nil || *item.Rank != index+1) {
			t.Errorf("%s: rank %v, position %d", item.Model, item.Rank, index)
		}
		if index >= 4 && item.Rank != nil {
			t.Errorf("%s: unknown rank %v", item.Model, item.Rank)
		}
	}
	if result.Models[2].Model != "warning" || result.Models[3].Model != "exhausted" {
		t.Fatalf("order = %+v", result.Models)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"auth_index", "credentials", "email", "private-key", "file_path"} {
		if secret == "credentials" {
			continue
		}
		if strings.Contains(string(encoded), secret) {
			t.Errorf("result contains private field %q", secret)
		}
	}
}

func TestModelAdviceBestCredentialAndUnknownProvenance(t *testing.T) {
	directory, quotas, credentials := adviceFixture()
	directory.Models = append(directory.Models, gateway.Model{ID: "ambiguous"}, gateway.Model{ID: "unmapped"})
	directory.Routes = append(directory.Routes,
		ModelSquareRoute{ProviderID: "oauth:claude", UpstreamModel: "claude-sonnet-4", CallPoint: "ambiguous"},
		ModelSquareRoute{ProviderID: "oauth:claude", UpstreamModel: "claude-opus-4", CallPoint: "ambiguous"})
	credentials = append(credentials, AdviceCredential{AuthIndex: "better", Provider: "claude", Models: []string{"warning"}})
	quotas = append(quotas, Quota{AuthIndex: "better", Provider: "claude", Status: "healthy", ObservedAtMS: 2_000_000_000_000, Windows: []quota.QuotaWindow{{Scope: "standard", UsedPercent: advicePercent(30), RemainingPercent: advicePercent(70)}}})
	result := ProjectModelAdvice(ModelAdviceInput{Models: []string{"warning", "ambiguous", "unmapped", "missing"}}, directory, quotas, credentials, 2_000_000_000_000, nil)
	if result.Models[0].Model != "warning" || result.Models[0].Verdict != "ok" || result.Models[0].CredentialsUsable != 2 {
		t.Fatalf("best credential = %+v", result.Models[0])
	}
	for _, item := range result.Models[1:] {
		if item.Verdict != "unknown" || item.Rank != nil {
			t.Errorf("unresolved route = %+v", item)
		}
	}
}

func TestModelAdviceRecommendationRequiresAUsableModel(t *testing.T) {
	directory, quotas, credentials := adviceFixture()
	for _, scenario := range []struct {
		models []string
		want   string
	}{
		{models: []string{"healthy", "warning"}, want: "healthy"},
		{models: []string{"warning", "exhausted"}, want: "warning"},
		{models: []string{"exhausted", "unread"}},
		{models: []string{"unread"}},
	} {
		result := ProjectModelAdvice(ModelAdviceInput{Models: scenario.models}, directory, quotas, credentials, 2_000_000_000_000, nil)
		if scenario.want == "" {
			if result.Recommended != nil {
				t.Errorf("%v: recommended %q without usable evidence", scenario.models, *result.Recommended)
			}
		} else if result.Recommended == nil || *result.Recommended != scenario.want {
			t.Errorf("%v: recommended %v, want %q", scenario.models, result.Recommended, scenario.want)
		}
	}
}

func TestModelAdviceStaleBlockedAndDisabledCredentials(t *testing.T) {
	directory, quotas, credentials := adviceFixture()
	credentials = append(credentials, AdviceCredential{AuthIndex: "disabled", Provider: "claude", IsDisabled: true, Models: []string{"healthy"}})
	quotas = append(quotas, Quota{AuthIndex: "disabled", Provider: "claude", Status: "healthy", Windows: []quota.QuotaWindow{{Scope: "standard", RemainingPercent: advicePercent(90), UsedPercent: advicePercent(10)}}})
	for index := range quotas {
		if quotas[index].AuthIndex == "healthy" {
			quotas[index].Status = "stale"
		}
	}
	result := ProjectModelAdvice(ModelAdviceInput{Models: []string{"healthy", "warning", "exhausted"}}, directory, quotas, credentials, 2_000_000_000_000, func(_, index string) bool { return index == "warning" })
	byModel := map[string]ModelAdviceItem{}
	for _, item := range result.Models {
		byModel[item.Model] = item
	}
	if byModel["healthy"].Verdict != "unknown" || byModel["healthy"].CredentialsTotal != 1 {
		t.Fatalf("stale and disabled = %+v", byModel["healthy"])
	}
	if byModel["warning"].Verdict != "avoid" || byModel["warning"].CredentialsUsable != 0 {
		t.Fatalf("parked = %+v", byModel["warning"])
	}
	if byModel["exhausted"].Verdict != "avoid" {
		t.Fatalf("exhausted = %+v", byModel["exhausted"])
	}
}

func TestModelAdviceAuthenticationFailureIsNotUsable(t *testing.T) {
	directory, quotas, credentials := adviceFixture()
	for index := range quotas {
		if quotas[index].AuthIndex == "unread" {
			quotas[index].Status = "error"
			quotas[index].Recommendation.Status = "needs_reauth"
		}
	}
	result := ProjectModelAdvice(ModelAdviceInput{Models: []string{"unread"}}, directory, quotas, credentials, 2_000_000_000_000, nil)
	if result.Models[0].Verdict != "unknown" || result.Models[0].CredentialsUsable != 0 {
		t.Fatalf("authentication failure = %+v", result.Models[0])
	}
}

func TestModelAdviceMissingCredentialMembershipRemainsUnmeasured(t *testing.T) {
	directory, quotas, credentials := adviceFixture()
	credentials = append(credentials, AdviceCredential{AuthIndex: "membership-unknown", Provider: "claude"})
	result := ProjectModelAdvice(ModelAdviceInput{Models: []string{"exhausted"}}, directory, quotas, credentials, 2_000_000_000_000, nil)
	item := result.Models[0]
	if item.Verdict != "unknown" || item.CredentialsTotal != 2 || item.CredentialsUsable != 0 {
		t.Fatalf("missing membership = %+v", item)
	}
	if !strings.Contains(strings.Join(item.Reasons, " "), "1 blocked, 1 without valid readings") {
		t.Fatalf("missing membership reasons = %v", item.Reasons)
	}
}

func TestModelAdviceDoesNotBorrowUnidentifiedConfigCredentials(t *testing.T) {
	directory, quotas, credentials := adviceFixture()
	directory.Providers[0].IsOAuth = false
	directory.Providers[0].ID = "claude-0"
	for index := range directory.Routes {
		directory.Routes[index].ProviderID = "claude-0"
	}
	result := ProjectModelAdvice(ModelAdviceInput{Models: []string{"healthy"}}, directory, quotas, credentials, 2_000_000_000_000, nil)
	if result.Models[0].Verdict != "unknown" || result.Models[0].CredentialsTotal != 0 {
		t.Fatalf("unidentified config credential = %+v", result.Models[0])
	}
}

func TestModelAdviceExactModelScope(t *testing.T) {
	directory, _, _ := adviceFixture()
	credentials := []AdviceCredential{{AuthIndex: "match", Provider: "claude", Models: []string{"healthy"}}}
	quotas := []Quota{{AuthIndex: "match", Status: "exhausted", ObservedAtMS: 2_000_000_000_000, Windows: []quota.QuotaWindow{
		{Scope: "group", ModelFamilies: []string{"claude-opus"}, UsedPercent: advicePercent(100), RemainingPercent: advicePercent(0)},
		{Scope: "model", Model: "claude-sonnet-4", UsedPercent: advicePercent(81), RemainingPercent: advicePercent(19)},
	}}}
	result := ProjectModelAdvice(ModelAdviceInput{Models: []string{"healthy"}}, directory, quotas, credentials, 2_000_000_000_000, nil)
	if result.Models[0].Verdict != "caution" || result.Models[0].TightestWindow == nil || result.Models[0].TightestWindow.UsedPercent != 81 {
		t.Fatalf("exact model scope = %+v", result.Models[0])
	}
}

func TestModelAdviceBoundedCatalogueAndInput(t *testing.T) {
	directory := ModelSquareDirectory{Models: []gateway.Model{}, Routes: []ModelSquareRoute{}, Providers: []ModelSquareProvider{}}
	for index := 0; index < 61; index++ {
		directory.Models = append(directory.Models, gateway.Model{ID: fmt.Sprintf("model-%02d", index)})
	}
	result := ProjectModelAdvice(ModelAdviceInput{}, directory, nil, nil, 2_000_000_000_000, nil)
	if len(result.Models) != 50 || result.Omitted != 11 {
		t.Fatalf("bounded result: %d listed, %d omitted", len(result.Models), result.Omitted)
	}
	large := ModelSquareDirectory{Models: []gateway.Model{}}
	for index := 0; index < 50; index++ {
		large.Models = append(large.Models, gateway.Model{ID: fmt.Sprintf("%03d-%s", index, strings.Repeat("x", 900))})
	}
	bounded := ProjectModelAdvice(ModelAdviceInput{}, large, nil, nil, 2_000_000_000_000, nil)
	encoded, _ := json.Marshal(bounded)
	if len(encoded) > 32*1024 || bounded.Omitted == 0 {
		t.Fatalf("byte budget: %d bytes, %d omitted", len(encoded), bounded.Omitted)
	}
}

func TestModelAdviceAdaptersAndReadOnlyPrivacy(t *testing.T) {
	ctx := context.Background()
	database, err := repository.Open(ctx, filepath.Join(t.TempDir(), "advice.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	repo := repository.New(database)
	directory, quotas, credentials := adviceFixture()
	quotas[0].Name = "account@example.com"
	quotas[0].AuthIndex = "fixture-auth-index"
	credentials[0].AuthIndex = "fixture-auth-index"
	service := &Service{Repo: repo, AdviceDirectory: func(context.Context) (ModelSquareDirectory, error) { return directory, nil },
		AdviceCredentials: func(context.Context) ([]AdviceCredential, error) { return credentials, nil },
		Quotas:            func(context.Context) ([]Quota, error) { return quotas, nil }}
	registry := capability.NewRegistry()
	if err := service.registerModelAdvice(registry); err != nil {
		t.Fatal(err)
	}
	definition, err := registry.Lookup("model_advice", capability.Principal{ID: "admin", Adapter: "agent", IsAdmin: true})
	if err != nil || definition.Metadata.Permission != "read" || definition.Metadata.Risk != "low" {
		t.Fatalf("declaration = %+v, %v", definition, err)
	}
	schema, err := json.Marshal(definition.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	var inputSchema struct {
		AdditionalProperties bool `json:"additionalProperties"`
		Properties           map[string]struct {
			Type any `json:"type"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(schema, &inputSchema); err != nil {
		t.Fatal(err)
	}
	if inputSchema.AdditionalProperties || len(inputSchema.Properties) != 1 || inputSchema.Properties["models"].Type == nil || len(inputSchema.Required) != 0 {
		t.Fatalf("input schema = %s", schema)
	}
	executor := &capability.Executor{Registry: registry, Store: repository.AgentStore{Repo: repo}, Authorize: func(context.Context, string, string) bool { return true }}
	for _, principal := range []capability.Principal{
		{ID: "admin", Adapter: "agent", IsAdmin: true},
		{ID: "readonly", Adapter: "mcp", IsReadOnly: true, Allowed: map[string]bool{"model_advice": true}},
	} {
		result, err := executor.Invoke(ctx, principal, "model_advice", json.RawMessage(`{"models":["healthy"]}`), "")
		if err != nil || result.Status != "success" {
			t.Fatalf("%s invocation = %+v, %v", principal.Adapter, result, err)
		}
		for _, secret := range []string{"account@example.com", "fixture-auth-index", "auth_index", "credential_name", "file_path"} {
			if strings.Contains(string(result.Data), secret) {
				t.Fatalf("%s leaked %q: %s", principal.Adapter, secret, result.Data)
			}
		}
	}
	tooMany := make([]string, 21)
	for index := range tooMany {
		tooMany[index] = fmt.Sprintf("model-%d", index)
	}
	raw, _ := json.Marshal(ModelAdviceInput{Models: tooMany})
	refusal, err := executor.Invoke(ctx, capability.Principal{ID: "admin", Adapter: "agent", IsAdmin: true}, "model_advice", raw, "")
	if err != nil || refusal.Status != "error" || refusal.Code != "invalid_parameters" {
		t.Fatalf("input bound = %+v, %v", refusal, err)
	}
	service.AdviceDirectory = func(context.Context) (ModelSquareDirectory, error) {
		return ModelSquareDirectory{}, errors.New("synthetic-directory-failure")
	}
	result, err := executor.Invoke(ctx, capability.Principal{ID: "admin", Adapter: "agent", IsAdmin: true}, "model_advice", json.RawMessage(`{"models":["healthy"]}`), "")
	if err != nil || result.Status != "success" {
		t.Fatalf("directory failure = %+v, %v", result, err)
	}
	var unavailable ModelAdviceResult
	if err := json.Unmarshal(result.Data, &unavailable); err != nil {
		t.Fatal(err)
	}
	if unavailable.Models[0].Verdict != "unknown" || unavailable.Models[0].Reasons[0] != "model_directory_unavailable" || unavailable.Recommended != nil {
		t.Fatalf("unavailable directory = %+v", unavailable)
	}
}

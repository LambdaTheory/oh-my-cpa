package quota

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/oh-my-cpa/oh-my-cpa/internal/cpa/management"
)

func pluginTestItems() []management.PluginItem {
	return []management.PluginItem{
		{ID: "fixture-limiter", Registered: true, Enabled: true, SupportsQuota: true, QuotaProvider: "codebuddy"},
		{ID: "fixture-meter", Registered: true, Enabled: true, SupportsQuota: true, QuotaProvider: "metered"},
		{ID: "fixture-logger", Registered: true, Enabled: true},
	}
}

// Built-ins win even when a plugin also advertises quota for the same provider:
// a Codex credential carrying CPA's quota advertisement stays on its reader.
func TestIsPluginQuotaFilePrefersBuiltIn(t *testing.T) {
	builtIn := management.AuthFile{Type: "codex", Provider: "codex", SupportsQuota: true, QuotaProvider: "codex"}
	if isPluginQuotaFile(builtIn) {
		t.Fatal("isPluginQuotaFile(codex) = true, want false")
	}
	plugin := management.AuthFile{Type: "codebuddy", Provider: "codebuddy", SupportsQuota: true, QuotaProvider: "codebuddy"}
	if !isPluginQuotaFile(plugin) {
		t.Fatal("isPluginQuotaFile(codebuddy) = false, want true")
	}
	plain := management.AuthFile{Type: "codebuddy", Provider: "codebuddy"}
	if isPluginQuotaFile(plain) {
		t.Fatal("isPluginQuotaFile without advertisement = true, want false")
	}
	probe := management.AuthFile{Type: "codebuddy", QuotaProbe: json.RawMessage(`{"kind":"declarative"}`)}
	if !isPluginQuotaFile(probe) {
		t.Fatal("isPluginQuotaFile(probe) = false, want true")
	}
}

func TestResolvePluginQuotaID(t *testing.T) {
	plugins := pluginTestItems()

	for _, testCase := range []struct {
		name     string
		provider string
		plugins  []management.PluginItem
		want     string
		wantErr  bool
	}{
		{name: "quota provider", provider: "codebuddy", plugins: plugins, want: "fixture-limiter"},
		{name: "plugin id", provider: "fixture-limiter", plugins: plugins, want: "fixture-limiter"},
		{name: "case insensitive", provider: "CodeBuddy", plugins: plugins, want: "fixture-limiter"},
		{
			name:     "oauth provider",
			provider: "codebuddy-oauth",
			plugins:  []management.PluginItem{{ID: "fixture-oauth", Registered: true, SupportsQuota: true, OAuthProvider: "codebuddy-oauth"}},
			want:     "fixture-oauth",
		},
		{
			name:     "unmatched single candidate refused",
			provider: "something-else",
			plugins:  []management.PluginItem{{ID: "only-one", Registered: true, SupportsQuota: true, QuotaProvider: "other"}},
			wantErr:  true,
		},
		{name: "empty provider", provider: "", plugins: plugins, wantErr: true},
		{name: "no match", provider: "unknown-provider", plugins: plugins, wantErr: true},
		{name: "unregistered ignored", provider: "ghost", plugins: []management.PluginItem{{ID: "ghost", SupportsQuota: true, QuotaProvider: "ghost"}}, wantErr: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := resolvePluginQuotaID(testCase.plugins, testCase.provider)
			if testCase.wantErr && err == nil {
				t.Fatalf("resolvePluginQuotaID(%q) succeeded, want an error", testCase.provider)
			}
			if !testCase.wantErr && (err != nil || got != testCase.want) {
				t.Fatalf("resolvePluginQuotaID(%q) = %q, %v; want %q", testCase.provider, got, err, testCase.want)
			}
		})
	}
}

// Declarative quota_probe credentials have no v8 management route: the failure
// names the gap instead of fetching against the wrong endpoint.
func TestPluginQuotaTargetRefusesDeclarativeProbe(t *testing.T) {
	svc := NewService(&mockCPAClient{})
	_, err := svc.pluginQuotaTarget(context.Background(), management.AuthFile{
		AuthIndex:  "auth-probe",
		Type:       "codebuddy",
		QuotaProbe: json.RawMessage(`{"kind":"declarative"}`),
	})
	if err == nil || !strings.Contains(err.Error(), "quota probe") {
		t.Fatalf("pluginQuotaTarget(probe) = %v, want the probe-unsupported error", err)
	}
}

// CPA's quota_plugin_id is the plugin it actually routes quota to. A name match
// would pick fixture-limiter here, which CPA may have fused; the target follows
// CPA without listing plugins at all.
func TestPluginQuotaTargetPrefersCPASelection(t *testing.T) {
	svc := NewService(&mockCPAClient{pluginsFunc: func(context.Context) ([]management.PluginItem, error) {
		t.Fatal("plugins listed although CPA named the quota plugin")
		return nil, nil
	}})
	got, err := svc.pluginQuotaTarget(context.Background(), management.AuthFile{
		AuthIndex:     "auth-selected",
		Type:          "codebuddy",
		SupportsQuota: true,
		QuotaProvider: "codebuddy",
		QuotaPluginID: "fixture-healthy",
	})
	if err != nil || got != "fixture-healthy" {
		t.Fatalf("pluginQuotaTarget = %q, %v; want fixture-healthy", got, err)
	}
}

const pluginQuotaFixture = `{
	"groups": [{
		"displayName": "Code models",
		"buckets": [{"bucketId": "5h", "displayName": "5 hour", "window": "5h", "remainingFraction": 0.4}]
	}],
	"subscription": {"plan": "Team", "tierName": "team-tier", "tierId": "team"}
}`

// A full refresh through the plugin branch projects groups onto windows and the
// subscription onto a plan, and advertises refresh support.
func TestRefreshCredentialQuotaPluginBranch(t *testing.T) {
	var calledPlugin, calledIndex string
	client := &mockCPAClient{
		pluginsFunc: func(ctx context.Context) ([]management.PluginItem, error) {
			return pluginTestItems(), nil
		},
		pluginQuotaFn: func(ctx context.Context, pluginID, authIndex string) (json.RawMessage, error) {
			calledPlugin, calledIndex = pluginID, authIndex
			return json.RawMessage(pluginQuotaFixture), nil
		},
	}

	result, err := NewService(client).RefreshCredentialQuota(context.Background(), management.AuthFile{
		AuthIndex:     "auth-plugin-1",
		Name:          "codebuddy.json",
		Type:          "codebuddy",
		Provider:      "codebuddy",
		SupportsQuota: true,
		QuotaProvider: "codebuddy",
	}, nil)
	if err != nil {
		t.Fatalf("RefreshCredentialQuota error: %v", err)
	}
	if calledPlugin != "fixture-limiter" || calledIndex != "auth-plugin-1" {
		t.Fatalf("fetch = (%q, %q), want (fixture-limiter, auth-plugin-1)", calledPlugin, calledIndex)
	}
	if !result.Capabilities.RefreshSupported || result.Capabilities.ResetCreditSupported {
		t.Fatalf("capabilities = %+v, want refresh without reset credit", result.Capabilities)
	}
	if len(result.Windows) != 1 || result.Windows[0].UsedPercent == nil || *result.Windows[0].UsedPercent != 60 {
		t.Fatalf("windows = %+v, want one window at 60%% used", result.Windows)
	}
	if result.Plan == nil || result.Plan.PlanLabel != "Team" {
		t.Fatalf("plan = %+v, want the Team label", result.Plan)
	}
	if result.Status != "healthy" && result.Status != "warning" {
		t.Fatalf("status = %q, want a successful reading", result.Status)
	}
}

// A stale reading: v0.3.0 writes the marker into the balance label and the
// bucket description, and the last-read instant into the description.
const pluginStaleBalancePayload = `{
	"subscription": {"plan": "Pro"},
	"summary": [{"key": "balance", "label": "Balance · stale", "value": 321, "format": "number", "unit": "credits"}],
	"groups": [{
		"displayName": "Command Code",
		"buckets": [
			{"window": "5h", "remainingFraction": 0.8, "resetTime": "2026-11-12T13:00:00Z", "description": "5-hour session · stale · last read at 2026-11-12T07:55:00Z · upstream request failed: 429 Too Many Requests"}
		]
	}]
}`

// The three providers publish different plan fields: Ollama only a currency
// balance, Command Code a plan plus a credits balance, OpenCode Go neither.
func TestParsePluginQuotaPlan(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		raw         string
		wantNil     bool
		wantLabel   string
		wantBalance string
		wantStale   bool
	}{
		{name: "ollama balance only", raw: pluginOllamaBalanceOnlyPayload, wantBalance: "5.5 USD"},
		{name: "ollama balance beside windows", raw: pluginOllamaCookiePayload, wantBalance: "12.34 USD"},
		{name: "command code plan and credits", raw: pluginCommandCodePayload, wantLabel: "Pro", wantBalance: "321 credits"},
		{name: "opencode windows only", raw: pluginOpenCodePayload, wantNil: true},
		{name: "stale balance label", raw: pluginStaleBalancePayload, wantLabel: "Pro", wantBalance: "321 credits", wantStale: true},
		{
			name:        "currency without a code stays the plain amount",
			raw:         `{"summary":[{"key":"balance","value":9,"format":"currency"}]}`,
			wantBalance: "9",
		},
		{
			name:        "number without a unit stays the plain amount",
			raw:         `{"summary":[{"key":"balance","value":7,"format":"number"}]}`,
			wantBalance: "7",
		},
		{
			name:        "first balance metric wins",
			raw:         `{"summary":[{"key":"other","value":1},{"key":"balance","value":2,"unit":"credits"},{"key":"balance","value":3,"unit":"credits"}]}`,
			wantBalance: "2 credits",
		},
		{name: "subscription without a balance", raw: `{"subscription":{"plan":"Team"}}`, wantLabel: "Team"},
		{name: "balance value unreadable", raw: `{"summary":[{"key":"balance","value":"n/a"}]}`, wantNil: true},
		{name: "invalid payload", raw: `{`, wantNil: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			plan := parsePluginQuotaPlan([]byte(testCase.raw))
			if testCase.wantNil {
				if plan != nil {
					t.Fatalf("parsePluginQuotaPlan = %+v, want nil", plan)
				}
				return
			}
			if plan == nil {
				t.Fatalf("parsePluginQuotaPlan = nil, want a plan (label %q, balance %q)", testCase.wantLabel, testCase.wantBalance)
			}
			// A plan without a PlanType is dropped by the read-time projection,
			// which would also lose the balance it carries.
			if plan.PlanType != "unknown" || plan.Tier != "unknown" {
				t.Errorf("plan type/tier = %q/%q, want unknown/unknown", plan.PlanType, plan.Tier)
			}
			if plan.PlanLabel != testCase.wantLabel {
				t.Errorf("plan label = %q, want %q", plan.PlanLabel, testCase.wantLabel)
			}
			if testCase.wantBalance == "" {
				if plan.Credits != nil {
					t.Errorf("credits = %+v, want none", plan.Credits)
				}
				return
			}
			if plan.Credits == nil || plan.Credits.Balance != testCase.wantBalance || plan.Credits.Stale != testCase.wantStale {
				t.Errorf("credits = %+v, want balance %q with stale=%v", plan.Credits, testCase.wantBalance, testCase.wantStale)
			}
		})
	}
}

// The api_key Ollama path publishes a balance and no window: the refresh keeps
// the balance on the plan, invents no window, and reads as unpublished because
// the provider stated no window.
func TestRefreshCredentialQuotaPluginBalanceOnly(t *testing.T) {
	client := &mockCPAClient{
		pluginQuotaFn: func(ctx context.Context, pluginID, authIndex string) (json.RawMessage, error) {
			if pluginID != "fixture-meter" {
				t.Fatalf("pluginID = %q, want CPA's quota_plugin_id", pluginID)
			}
			return json.RawMessage(pluginOllamaBalanceOnlyPayload), nil
		},
	}
	result, err := NewService(client).RefreshCredentialQuota(context.Background(), management.AuthFile{
		AuthIndex:     "auth-ollama-key",
		Type:          "ollama",
		SupportsQuota: true,
		QuotaProvider: "ollama",
		QuotaPluginID: "fixture-meter",
	}, nil)
	if err != nil {
		t.Fatalf("RefreshCredentialQuota error: %v", err)
	}
	if result.Plan == nil || result.Plan.Credits == nil || result.Plan.Credits.Balance != "5.5 USD" {
		t.Fatalf("plan = %+v, want the 5.5 USD balance kept", result.Plan)
	}
	if len(result.Windows) != 0 {
		t.Fatalf("windows = %+v, want none", result.Windows)
	}
	if result.Status != QuotaStatusUnpublished {
		t.Fatalf("status = %q, want %q for a balance-only read", result.Status, QuotaStatusUnpublished)
	}
}

// An unresolvable plugin reads as an error, never as an empty success.
func TestRefreshCredentialQuotaPluginUnresolvable(t *testing.T) {
	client := &mockCPAClient{
		pluginsFunc: func(ctx context.Context) ([]management.PluginItem, error) {
			return nil, nil
		},
	}
	result, err := NewService(client).RefreshCredentialQuota(context.Background(), management.AuthFile{
		AuthIndex:     "auth-plugin-9",
		Type:          "codebuddy",
		SupportsQuota: true,
		QuotaProvider: "codebuddy",
	}, nil)
	if err != nil {
		t.Fatalf("RefreshCredentialQuota error: %v", err)
	}
	if result.Status != "error" || result.Error == "" {
		t.Fatalf("result = %+v, want an error reading", result)
	}
}

func TestCapabilitiesForFileFollowsTheQuotaReader(t *testing.T) {
	plugin := management.AuthFile{Type: "codebuddy", Provider: "codebuddy", SupportsQuota: true, QuotaProvider: "codebuddy"}
	if caps := CapabilitiesForFile(plugin); !caps.RefreshSupported {
		t.Fatalf("plugin capabilities = %+v, want refresh supported", caps)
	}
	builtIn := management.AuthFile{Type: "codex", Provider: "codex"}
	if caps := CapabilitiesForFile(builtIn); !caps.RefreshSupported || !caps.ResetCreditSupported {
		t.Fatalf("codex capabilities = %+v, want refresh and reset credit", caps)
	}
	plain := management.AuthFile{Type: "codebuddy", Provider: "codebuddy"}
	if caps := CapabilitiesForFile(plain); caps.RefreshSupported {
		t.Fatalf("unadvertised capabilities = %+v, want no refresh", caps)
	}
}

func TestRefreshCredentialQuotaRejectsUnrelatedPlugin(t *testing.T) {
	hasCalledQuotaPlugin := false
	client := &mockCPAClient{
		pluginsFunc: func(ctx context.Context) ([]management.PluginItem, error) {
			return []management.PluginItem{{ID: "unrelated", Registered: true, SupportsQuota: true, QuotaProvider: "other"}}, nil
		},
		pluginQuotaFn: func(ctx context.Context, pluginID, authIndex string) (json.RawMessage, error) {
			hasCalledQuotaPlugin = true
			return json.RawMessage(pluginQuotaFixture), nil
		},
	}
	result, err := NewService(client).RefreshCredentialQuota(context.Background(), management.AuthFile{
		AuthIndex: "auth-plugin-unmatched", Type: "codebuddy", SupportsQuota: true, QuotaProvider: "codebuddy",
	}, nil)
	if err != nil {
		t.Fatalf("RefreshCredentialQuota error: %v", err)
	}
	if hasCalledQuotaPlugin {
		t.Fatal("unrelated plugin received the credential auth index")
	}
	if result.Status != "error" || result.Error == "" || len(result.Windows) != 0 || result.Plan != nil {
		t.Fatalf("result = %+v, want an error without unrelated windows or plan", result)
	}
}

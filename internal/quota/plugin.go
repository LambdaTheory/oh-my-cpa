package quota

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/oh-my-cpa/oh-my-cpa/internal/cpa/management"
)

// builtInQuotaProviders are the credentials with a dedicated quota reader in
// RefreshCredentialQuota. A plugin card is generic by definition: built-ins win
// even when a plugin also advertises quota for the same provider.
var builtInQuotaProviders = map[string]bool{
	"codex": true, "claude": true, "antigravity": true, "kimi": true,
	"xai": true, "meta": true, "devin": true,
}

// pluginQuotaCapabilities is what a plugin-provided credential supports: live
// refresh and cooldown clearing, but no reset credit and no probe of its own.
func pluginQuotaCapabilities() QuotaCapabilities {
	return QuotaCapabilities{
		RefreshSupported:       true,
		ClearCooldownSupported: true,
		ResetCreditSupported:   false,
	}
}

// isPluginQuotaFile reports a credential whose quota comes from a plugin: a
// non-built-in provider carrying CPA's quota advertisement (`supports_quota` /
// `quota_provider`, or a declarative `quota_probe` that resolves to an explicit
// unsupported message instead of a fetch).
func isPluginQuotaFile(file management.AuthFile) bool {
	if builtInQuotaProviders[DetectProvider(file.Type, file.Provider)] {
		return false
	}
	if len(nonNullJSON(file.QuotaProbe)) > 0 {
		return true
	}
	return file.SupportsQuota || strings.TrimSpace(file.QuotaProvider) != ""
}

func nonNullJSON(raw json.RawMessage) json.RawMessage {
	if len(strings.TrimSpace(string(raw))) == 0 || strings.TrimSpace(string(raw)) == "null" {
		return nil
	}
	return raw
}

// resolvePluginQuotaID maps a credential's advertised quota provider to the
// installed plugin that serves it (quota identifier, plugin id, then auth
// provider). An unmatched credential must not be sent to an unrelated plugin,
// even when it is the only registered quota plugin.
func resolvePluginQuotaID(plugins []management.PluginItem, provider string) (string, error) {
	target := strings.ToLower(strings.TrimSpace(provider))
	if target == "" {
		return "", errors.New("credential does not advertise a quota provider")
	}
	candidates := make([]management.PluginItem, 0)
	for _, plugin := range plugins {
		if plugin.Registered && plugin.SupportsQuota {
			candidates = append(candidates, plugin)
		}
	}
	matchID := func(want func(management.PluginItem) string) string {
		for _, plugin := range candidates {
			if strings.ToLower(strings.TrimSpace(want(plugin))) == target {
				return plugin.ID
			}
		}
		return ""
	}
	if id := matchID(func(plugin management.PluginItem) string { return plugin.QuotaProvider }); id != "" {
		return id, nil
	}
	if id := matchID(func(plugin management.PluginItem) string { return plugin.ID }); id != "" {
		return id, nil
	}
	if id := matchID(func(plugin management.PluginItem) string { return plugin.OAuthProvider }); id != "" {
		return id, nil
	}
	return "", fmt.Errorf("no enabled plugin provides quota for %q", provider)
}

// pluginQuotaTarget resolves the plugin id serving a plugin-quota credential.
// CPA's own selection wins when the gateway reports it: matching names here
// cannot see which plugin CPA has fused, so it could pick one CPA bypasses.
// Gateways older than v8.0.25 report no selection and fall back to the match.
// Declarative quota_probe credentials have no v8 management route and report an
// explicit unsupported message instead of failing a fetch.
func (s *Service) pluginQuotaTarget(ctx context.Context, file management.AuthFile) (string, error) {
	if pluginID := strings.TrimSpace(file.QuotaPluginID); pluginID != "" {
		return pluginID, nil
	}
	if s.client == nil {
		return "", errors.New("CPA client is not configured")
	}
	if len(nonNullJSON(file.QuotaProbe)) > 0 && strings.TrimSpace(file.QuotaProvider) == "" {
		return "", errors.New("declarative quota probes are not available through the v8 management API")
	}
	plugins, err := s.client.Plugins(ctx)
	if err != nil {
		return "", err
	}
	return resolvePluginQuotaID(plugins, file.QuotaProvider)
}

// fetchPluginQuota reads a plugin quota payload and projects it onto the
// shared windows/plan shape. Plugin quotas reuse the Antigravity groups shape
// (groups of buckets) with a plan/tier subscription and a summary beside it;
// the plugin's description text carries the window names and the stale marker.
// stale reports that the figures are the plugin's last successful reading and
// observedAtMS when that reading was taken (0 when the payload states none).
func (s *Service) fetchPluginQuota(ctx context.Context, pluginID, authIndex string, nowMS int64) (windows []QuotaWindow, plan *QuotaPlan, stale bool, observedAtMS int64, err error) {
	if s.client == nil {
		return nil, nil, false, 0, errors.New("CPA client is not configured")
	}
	raw, err := s.client.FetchPluginQuota(ctx, pluginID, authIndex)
	if err != nil {
		return nil, nil, false, 0, err
	}
	windows, observedAtMS, stale, err = ParsePluginQuotaWindows(raw, nowMS, 0)
	if err != nil {
		return nil, nil, false, 0, err
	}
	return windows, parsePluginQuotaPlan(raw), stale, observedAtMS, nil
}

// pluginQuotaMetric is one entry of a plugin payload's summary[]: a figure the
// plugin states beside the plan, such as the account's balance.
type pluginQuotaMetric struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Value    any    `json:"value"`
	Format   string `json:"format"`
	Currency string `json:"currency"`
	Unit     string `json:"unit"`
}

// parsePluginQuotaPlan projects a plugin quota payload's plan fields onto a
// plan: the subscription's display strings and the summary's balance. The
// payload carries display strings only (no active flag, no windows), so tier
// stays unknown rather than guessing elite/premium from a name.
//
// A payload with a balance but no plan name still returns a plan: the balance
// lives on the plan, and the read-time projection drops a plan without a
// PlanType, which would lose the only figure the provider published.
func parsePluginQuotaPlan(raw []byte) *QuotaPlan {
	var payload struct {
		Subscription *struct {
			Plan     string `json:"plan"`
			TierName string `json:"tierName"`
			TierID   string `json:"tierId"`
		} `json:"subscription"`
		Summary []pluginQuotaMetric `json:"summary"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return nil
	}
	label := ""
	if payload.Subscription != nil {
		label = strings.TrimSpace(payload.Subscription.Plan)
		if label == "" {
			label = strings.TrimSpace(payload.Subscription.TierName)
		}
		if label == "" {
			label = strings.TrimSpace(payload.Subscription.TierID)
		}
	}
	credits := parsePluginQuotaBalance(payload.Summary)
	if label == "" && credits == nil {
		return nil
	}
	return &QuotaPlan{PlanType: "unknown", PlanLabel: label, Tier: "unknown", Credits: credits}
}

// parsePluginQuotaBalance projects the first summary metric keyed "balance"
// onto the credits slot. The balance keeps the plugin's own unit as text —
// "12.34 USD" for a stated currency, "321 credits" for a plain number — because
// the display slot is a text field and the currency is not always money.
// A label containing "stale" (v0.3.0 writes "Balance · stale") marks the
// figure as the plugin's cached reading.
func parsePluginQuotaBalance(metrics []pluginQuotaMetric) *QuotaCredits {
	for _, metric := range metrics {
		if strings.TrimSpace(metric.Key) != "balance" {
			continue
		}
		amount, ok := toFloat(metric.Value)
		if !ok {
			return nil
		}
		balance := formatPluginBalanceAmount(amount)
		if strings.EqualFold(strings.TrimSpace(metric.Format), "currency") {
			if currency := strings.TrimSpace(metric.Currency); currency != "" {
				balance += " " + strings.ToUpper(currency)
			}
		} else if unit := strings.TrimSpace(metric.Unit); unit != "" {
			balance += " " + unit
		}
		return &QuotaCredits{
			Balance: balance,
			Stale:   strings.Contains(strings.ToLower(metric.Label), "stale"),
		}
	}
	return nil
}

// formatPluginBalanceAmount renders a balance with at most two decimals and no
// trailing zeros: 12.34, 5.5, 321. A figure the plugin states as a whole number
// must not read as 321.00, and one with cents must not be rounded away.
func formatPluginBalanceAmount(value float64) string {
	text := strconv.FormatFloat(value, 'f', 2, 64)
	text = strings.TrimRight(text, "0")
	text = strings.TrimRight(text, ".")
	return text
}

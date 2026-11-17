package quota

import "testing"

// Command Code's billing family and OpenCode's exact usage endpoint are the two
// new ways a credential-bearing api-call can leave OMC. Each must stay bound to
// the host and path it was added for.
func TestCommandCodeAndOpenCodeAllowlistEntriesAreBound(t *testing.T) {
	for _, goodURL := range []string{
		CommandCodeCreditsURL,
		CommandCodeSubscriptionsURL,
		CommandCodeBillingPrefix + "whoami?limits=1",
		OpenCodeUsageURL,
		OpenCodeUsageURL + "?include=windows",
	} {
		if !IsAllowedQuotaURL(goodURL) {
			t.Errorf("IsAllowedQuotaURL(%q) = false, want true", goodURL)
		}
	}

	for _, badURL := range []string{
		// Command Code: another path on the same host, another host, another scheme.
		"https://api.commandcode.ai/alpha/billing-evil/credits",
		"https://api.commandcode.ai/alpha/other/credits",
		"https://api.commandcode.ai.evil.test/alpha/billing/credits",
		"https://api.commandcode.ai:8443/alpha/billing/credits",
		"http://api.commandcode.ai/alpha/billing/credits",
		"https://API.commandcode.ai/alpha/billing/credits",
		"https://api.commandcode.ai/alpha/billing/../secrets",
		"https://api.commandcode.ai/alpha/billing/./credits",
		// OpenCode: the exact endpoint is allowed, not every path on the host or
		// every spelling of the host.
		"https://opencode.ai/zen/go/v1/usage/extra",
		"https://opencode.ai/zen/go/v1/usage-extra",
		"https://opencode.ai/zen/v1/usage",
		"https://opencode.ai/zen/go/v1/../../secrets",
		"https://opencode.ai.evil.test/zen/go/v1/usage",
		"https://opencode.ai:8443/zen/go/v1/usage",
		"http://opencode.ai/zen/go/v1/usage",
		"https://OPENCODE.ai/zen/go/v1/usage",
	} {
		if IsAllowedQuotaURL(badURL) {
			t.Errorf("IsAllowedQuotaURL(%q) = true, want false", badURL)
		}
	}
}

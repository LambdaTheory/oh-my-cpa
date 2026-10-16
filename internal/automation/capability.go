package automation

import (
	"strings"

	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

// Account-level weekly capability registry.
//
// A provider's "account-level 7d window is exhausted" claim is only acted on when
// OMC has verified how that provider publishes the window. This file names what is
// verified today. It is a capability registry, not a two-provider whitelist: a
// provider is added deliberately once its account-level weekly window - the kind it
// is published under and the exact period it meters - has been confirmed. A provider
// that is not in the registry has no verified mapping yet and stays monitor-only,
// rather than being treated as a provider whose any seven-day-shaped window counts.

// accountWeeklyCapability is one provider's verified account-level weekly window:
// the window kinds the provider publishes for it and the exact period in minutes.
type accountWeeklyCapability struct {
	provider string
	kinds    map[string]bool
	minutes  int
}

// verifiedWeeklyCapabilities is the registry of account-level weekly windows OMC has
// verified. Claude's account-level weekly window is known by its own mapping (the
// provider's seven_day id, normalized to the shared weekly kind); Codex's is known by
// the real duration it meters. Both are a real seven days (10080 minutes) exactly, not
// a 168h ± 1h reading, and neither is inferred from a different provider.
var verifiedWeeklyCapabilities = []accountWeeklyCapability{
	{provider: "claude", kinds: map[string]bool{"weekly": true, "seven_day": true}, minutes: 10080},
	{provider: "codex", kinds: map[string]bool{"weekly": true}, minutes: 10080},
}

// verifiedWeeklyCapability resolves a provider to its verified account-level weekly
// capability. An empty provider is never assumed to be any known provider.
func verifiedWeeklyCapability(provider string) (accountWeeklyCapability, bool) {
	normalized := strings.ToLower(strings.TrimSpace(provider))
	if normalized == "" {
		return accountWeeklyCapability{}, false
	}
	for _, capability := range verifiedWeeklyCapabilities {
		if capability.provider == normalized {
			return capability, true
		}
	}
	return accountWeeklyCapability{}, false
}

// ProviderHasVerifiedWeeklyCapability reports whether provider's account-level weekly
// window is a capability OMC has verified. It is the public face of the registry for
// the API's candidate eligibility: a provider that is not verified - including an
// unknown or empty one - must not be offered as able to be auto-managed yet.
func ProviderHasVerifiedWeeklyCapability(provider string) bool {
	_, ok := verifiedWeeklyCapability(provider)
	return ok
}

// sameProvider compares two provider labels the way the registry keys them.
func sameProvider(left, right string) bool {
	return strings.EqualFold(strings.TrimSpace(left), strings.TrimSpace(right))
}

// admitWeeklyEvidence is the single admission rule shared by every path that acts on
// an account-level weekly reading: the live-credential disable and the disabled
// takeover's recovery plan. It validates the whole identity chain and the capability
// at once, so the two paths cannot drift apart.
//
// The roster account, the evidence reading and the live gateway credential must name
// the same credential (auth index) and the same provider, and that provider must have
// a verified account-level weekly capability whose kind and exact period the reading
// states. A failure yields the canonical monitor reason: unverified_account_weekly_capability
// when the provider has no verified mapping - so an unknown provider that publishes a
// weekly 100 is still not acted on - and evidence_identity_mismatch when the account,
// the reading and the live credential disagree, so a credential whose provider changed
// under the same auth index is never closed against, or enabled from, another provider's
// reading.
func admitWeeklyEvidence(account repository.AutomationAccount, evidence repository.AutomationWeeklyWindow, credentialProvider string) (string, bool) {
	capability, verified := verifiedWeeklyCapability(account.Provider)
	if !verified {
		return ReasonUnverifiedCapability, false
	}
	if !sameProvider(account.Provider, credentialProvider) {
		return ReasonIdentityMismatch, false
	}
	if !sameProvider(account.Provider, evidence.Provider) {
		return ReasonIdentityMismatch, false
	}
	if strings.TrimSpace(account.AuthIndex) != strings.TrimSpace(evidence.AuthIndex) {
		return ReasonIdentityMismatch, false
	}
	if !capability.kinds[strings.ToLower(strings.TrimSpace(evidence.WindowKind))] {
		return ReasonUnverifiedCapability, false
	}
	if evidence.WindowMinutes != capability.minutes {
		return ReasonUnverifiedCapability, false
	}
	return "", true
}

// admitRecoveryIdentity validates an already-saved recovery plan before it is executed.
// It checks identity and capability only - the plan's reset instant was validated when
// it was saved - so a qualified plan is still enabled directly at its reset with no
// probe. A plan saved for one provider is never executed against a credential whose
// provider changed under the same auth index, and a provider without a verified weekly
// capability is never enabled from a plan or a hand-filled time.
func admitRecoveryIdentity(account repository.AutomationAccount, credentialProvider string) (string, bool) {
	if !ProviderHasVerifiedWeeklyCapability(account.Provider) {
		return ReasonUnverifiedCapability, false
	}
	if !sameProvider(account.Provider, credentialProvider) {
		return ReasonIdentityMismatch, false
	}
	return "", true
}

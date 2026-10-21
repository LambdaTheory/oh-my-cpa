package reauth

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/oh-my-cpa/oh-my-cpa/internal/cpa/management"
)

type Provider struct {
	ID, Name, Flow, CodeKind string
}

// ProviderFor uses the built-in login registry as the recovery boundary. Plugin
// names and quota-family substring matching cannot authorize a login job.
func ProviderFor(id string) (Provider, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, entry := range management.OAuthProviders {
		canonical := entry.LoginProvider
		if canonical == "" {
			canonical = entry.ID
		}
		if id != entry.ID && id != canonical {
			continue
		}
		p := Provider{ID: canonical, Name: entry.Name, CodeKind: "none"}
		switch {
		case entry.Flow == management.OAuthFlowDevice:
			p.Flow, p.CodeKind = "device_code", "device"
		case canonical == "claude" || canonical == "codex":
			p.Flow, p.CodeKind = "redirect_otp", "otp"
		case entry.UsesLoopbackCallback:
			p.Flow = "redirect_browser"
		default:
			p.Flow = "redirect_manual_callback"
		}
		return p, true
	}
	return Provider{}, false
}

// CredentialProvider preserves CPA's exact account system (including kimi-ai)
// and refuses explicit non-OAuth credential types even with an OAuth quota family.
func CredentialProvider(fileType, provider, authIndex string) (Provider, bool) {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(authIndex)), "api-keys.") || strings.HasPrefix(strings.ToLower(strings.TrimSpace(provider)), "api-keys.") {
		return Provider{}, false
	}
	if strings.TrimSpace(fileType) != "" {
		return ProviderFor(fileType)
	}
	return ProviderFor(provider)
}

type OAuthStart struct {
	Flow            string `json:"flow"`
	UserCode        string `json:"user_code"`
	VerificationURL string `json:"verification_url"`
	ExpiresIn       int64  `json:"expires_in"`
}

var DEVICE_USER_CODE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 -]{0,63}$`)

func validOAuthStart(p Provider, start OAuthStart) bool {
	flow := "redirect"
	if p.CodeKind == "device" {
		flow = "device"
	}
	if start.Flow != "" && start.Flow != flow && start.Flow != p.Flow {
		return false
	}
	if p.CodeKind != "device" {
		return start.UserCode == "" && start.VerificationURL == "" && start.ExpiresIn == 0
	}
	if !DEVICE_USER_CODE.MatchString(start.UserCode) || start.ExpiresIn <= 0 || start.ExpiresIn > 86400 {
		return false
	}
	u, err := url.Parse(start.VerificationURL)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.RawQuery != "" || u.Fragment != "" || len(start.VerificationURL) > 512 {
		return false
	}
	// The authenticated runner supplies a human verification page, never an
	// outbound API target. Query/fragment data and markup are not display metadata.
	return u.Hostname() != "" && !strings.ContainsAny(start.VerificationURL, "<>\" \t\r\n")
}

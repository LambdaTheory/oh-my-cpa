package quota

import (
	"encoding/json"
	"errors"

	"github.com/oh-my-cpa/oh-my-cpa/internal/authfailure"
	"github.com/oh-my-cpa/oh-my-cpa/internal/cpa/management"
)

func quotaErrorSource(endpoint string) authfailure.Source {
	switch endpoint {
	case CodexUsageURL, ClaudeUsageURL, AntigravityQuotaURLDaily, AntigravityQuotaURLSandbox, AntigravityQuotaURLCloud, KimiUsageURL, KimiInternationalUsageURL, XaiBillingWeeklyURL, XaiBillingMonthlyURL, XaiApiMeURL, MetaUsageURL, DevinSeatStatusURL:
		return authfailure.UpstreamQuota
	case XaiApiChatURL:
		return authfailure.Inference
	default:
		return authfailure.Supplemental
	}
}

func responseFailure(endpoint string, response management.ApiCallResponse) error {
	body, err := response.NormalizedBody()
	if err != nil {
		if response.StatusCode >= 400 {
			return authfailure.Parse(quotaErrorSource(endpoint), endpoint, response.StatusCode, nil)
		}
		return errors.New("invalid upstream payload: decode failed")
	}
	evidence := authfailure.Parse(quotaErrorSource(endpoint), endpoint, response.StatusCode, body)
	if response.StatusCode < 200 || response.StatusCode >= 300 || evidence.HasEnvelope || evidence.NeedsReauth() {
		return evidence
	}
	if evidence.Source == authfailure.UpstreamQuota && !isRecognizedQuotaPayload(endpoint, body) {
		return errors.New("invalid upstream payload: no recognized quota observation")
	}
	return nil
}

// Recognized empty collections are published absence; an empty or unrelated
// document cannot prove that the credential was accepted by a quota endpoint.
func isRecognizedQuotaPayload(endpoint string, body []byte) bool {
	var object map[string]json.RawMessage
	if json.Unmarshal(body, &object) != nil {
		return false
	}
	var keys []string
	switch endpoint {
	case CodexUsageURL:
		keys = []string{"plan_type", "rate_limit", "code_review_rate_limit", "additional_rate_limits"}
	case ClaudeUsageURL:
		return isRecognizedClaudeUsage(body)
	case AntigravityQuotaURLDaily, AntigravityQuotaURLSandbox, AntigravityQuotaURLCloud:
		keys = []string{"groups"}
	case KimiUsageURL, KimiInternationalUsageURL:
		keys = []string{"usage", "usages", "limits"}
	case XaiBillingWeeklyURL, XaiBillingMonthlyURL:
		keys = []string{"config"}
	case XaiApiMeURL:
		keys = []string{"id", "user_id", "userId", "api_key_id"}
	case MetaUsageURL:
		keys = []string{"subs_usage", "subs_tier_name", "is_subs_active"}
	case DevinSeatStatusURL:
		keys = []string{"userStatus", "user_status"}
	default:
		return true
	}
	for _, key := range keys {
		if _, ok := object[key]; ok {
			return true
		}
	}
	return false
}

func failureEvidence(err error) *authfailure.Evidence {
	var evidence *authfailure.Evidence
	if errors.As(err, &evidence) {
		return evidence
	}
	if err == nil {
		return nil
	}
	return &authfailure.Evidence{Source: authfailure.Transport, Message: err.Error()}
}

// Fallback endpoints may add evidence but cannot erase authentication rejection
// or a backoff instruction with a later, less specific failure.
func preferFailure(current, candidate error) error {
	if current == nil {
		return candidate
	}
	if candidate == nil {
		return current
	}
	if rateLimitFrom(current) != nil {
		return current
	}
	if rateLimitFrom(candidate) != nil {
		return candidate
	}
	if failureEvidence(current).NeedsReauth() {
		return current
	}
	if failureEvidence(candidate).NeedsReauth() {
		return candidate
	}
	return current
}

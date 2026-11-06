package feishu

import "time"

// A later non-auth failure can explain missing data without resolving an older
// auth incident. Fresh explicit rejections still take the credential-policy path.
func hasNonAuthCollectionFailure(account AccountReport, now time.Time) bool {
	return account.CollectionFailed && account.AttemptReason != "auth" && !account.HasFreshAuthFailure(now)
}

// Evidence sources identify independent clocks, not interchangeable report times.
const (
	EvidenceQuota          = "quota_observation"
	EvidenceAuthentication = "authentication_signal"
	EvidenceAttempt        = "collection_attempt"
	EvidenceMonitoring     = "monitoring_policy"
)

package feishu

// Evidence sources identify independent clocks, not interchangeable report times.
const (
	EvidenceQuota          = "quota_observation"
	EvidenceAuthentication = "authentication_signal"
	EvidenceAttempt        = "collection_attempt"
	EvidenceMonitoring     = "monitoring_policy"
)

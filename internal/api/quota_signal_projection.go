package api

import (
	"github.com/oh-my-cpa/oh-my-cpa/internal/authfailure"
	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

// Failures do not replace successful snapshots. Read surfaces must overlay the
// durable collection signal, just as Feishu does, before deriving guidance.
func applyQuotaCollectionSignal(q *quota.NormalizedQuota, signal repository.FeishuAccountSignal) {
	switch signal.State {
	case repository.FeishuSignalHealthy:
		if !signal.ObservedAt.IsZero() && signal.ObservedAt.UnixMilli() > q.LastAuthenticatedReadAtMS {
			q.LastAuthenticatedReadAtMS = signal.ObservedAt.UnixMilli()
		}
	case repository.FeishuSignalAuthInvalid:
		if !signal.ObservedAt.IsZero() && q.LastAuthenticatedReadAtMS > signal.ObservedAt.UnixMilli() {
			return
		}
		q.Failure = &authfailure.Evidence{Source: authfailure.UpstreamQuota, Code: "invalid_token"}
		q.Error = "authentication failed"
	case repository.FeishuSignalTransport, repository.FeishuSignalParse, repository.FeishuSignalUnknown:
		q.Failure = &authfailure.Evidence{Source: authfailure.Transport}
		q.Error = "quota refresh failed"
		if len(q.Windows) > 0 {
			q.Status = "stale"
		}
	}
}

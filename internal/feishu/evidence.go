package feishu

import (
	"strings"
	"time"
)

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
	EvidenceAvailability   = "channel_availability"
)

// alertEvidenceFresh rejects unknown clocks/bounds, including legacy payloads.
// Monitoring is not an age-bounded sample: only a new policy evaluation can renew it.
func alertEvidenceFresh(alert Alert, now time.Time) bool {
	if alert.FreshWindowMS <= 0 || alert.FreshWindowMS > int64((1<<63-1)/int64(time.Millisecond)) {
		return false
	}
	switch {
	case alert.Kind == AlertCredential:
		if alert.EvidenceSource != EvidenceAuthentication {
			return false
		}
	case alert.Kind == AlertReadingMissing:
		if alert.EvidenceSource != EvidenceQuota && alert.EvidenceSource != EvidenceAttempt {
			return false
		}
	case alert.Kind == AlertChannelUnavailable:
		if alert.EvidenceSource != EvidenceAvailability {
			return false
		}
	case alert.Kind == AlertQuotaLow || alert.Kind == AlertQuotaExhausted || strings.HasPrefix(alert.Kind, predictionKindPrefix):
		if alert.EvidenceSource != EvidenceQuota {
			return false
		}
	default:
		return false
	}
	return !alertStale(alert, now)
}

func currentEvidenceAlerts(report *Report, tracks map[string]ChannelTrack, cfg PolicyConfig, now time.Time) map[string]Alert {
	current := map[string]Alert{}
	if report == nil {
		return current
	}
	for _, alert := range attentionSignals(*report, cfg.TightRemainingPercent, now) {
		current[alertItemID(alert)] = alert
	}
	if cfg.ChannelUnavailableAfter <= 0 {
		cfg.ChannelUnavailableAfter = DefaultChannelUnavailableAfter
	}
	if cfg.MonitorDownAfter <= 0 {
		cfg.MonitorDownAfter = DefaultMonitorDownAfter
	}
	_, policy := policySignals(analyzeReport(cfg, *report, now, tracks), now)
	for id, alert := range policy {
		current[id] = *alert
	}
	return current
}

// eligibleNotificationAlerts is shared by enqueue and every actual send. Pending
// ownership can select a current report item, but can never supply its evidence.
func eligibleNotificationAlerts(notification Notification, report *Report, tracks map[string]ChannelTrack, cfg PolicyConfig, now time.Time) []Alert {
	current := currentEvidenceAlerts(report, tracks, cfg, now)
	candidates := append([]Alert(nil), notification.Alerts...)
	selected := map[string]bool{}
	for _, alert := range candidates {
		selected[alertItemID(alert)] = true
	}
	selectionIDs := append([]string(nil), notification.ItemIDs...)
	for _, pending := range notification.Pending {
		selectionIDs = append(selectionIDs, pending.ID)
	}
	// A selected identity without a stored alert was reserved by this row's own
	// creation instant, not by whenever a retry re-reads the report. Keeping the
	// instant stable lets a later discard match the pending item's NotifiedAt.
	claimAt := notification.OccurredAt
	if !isMonitoringTimeValid(claimAt) {
		claimAt = notification.CreatedAt
	}
	for _, id := range selectionIDs {
		if alert, ok := current[id]; ok && !selected[id] {
			if isMonitoringTimeValid(claimAt) {
				alert.OccurredAt = claimAt
			}
			candidates = append(candidates, alert)
			selected[id] = true
		}
	}
	var eligible []Alert
	seen := map[string]bool{}
	for _, alert := range candidates {
		switch alert.Kind {
		case statusHealthy:
			if report == nil || !reportEvidenceHealthy(*report, now) {
				alert = unknownDataAlert(now)
			}
		case statusUnknown, statusReported, "omitted":
			// These are presentation markers, never quota/authentication evidence.
		case AlertMonitorDown:
			refreshed, exists := current[alertItemID(alert)]
			if !exists || refreshed.Kind != AlertMonitorDown {
				continue
			}
			alert = retainClaimInstant(refreshed, alert)
		default:
			if !strings.HasPrefix(alert.Kind, predictionKindPrefix) && report != nil {
				refreshed, exists := current[alertItemID(alert)]
				if !exists {
					continue
				}
				// Re-reading the same observation cannot extend a narrower bound.
				// A genuinely newer observation may qualify the retained episode again.
				if alert.ObservedAt.Equal(refreshed.ObservedAt) && alert.FreshWindowMS > 0 && alert.FreshWindowMS < refreshed.FreshWindowMS {
					refreshed.FreshWindowMS = alert.FreshWindowMS
				}
				alert = retainClaimInstant(refreshed, alert)
			}
			if !alertEvidenceFresh(alert, now) {
				continue
			}
		}
		id := alertItemID(alert)
		if !seen[id] {
			seen[id] = true
			eligible = append(eligible, alert)
		}
	}
	return eligible
}

// The claim instant belongs to the reservation, not to the evidence. A retry
// re-reads the report and requalifies the alert, but moving OccurredAt would
// make a later discard fail to match the pending item's frozen NotifiedAt.
func retainClaimInstant(refreshed, stored Alert) Alert {
	if isMonitoringTimeValid(stored.OccurredAt) {
		refreshed.OccurredAt = stored.OccurredAt
	}
	return refreshed
}

func reportEvidenceHealthy(report Report, now time.Time) bool {
	hasAccount := false
	for _, provider := range report.Providers {
		if provider.Error != "" || len(provider.Accounts) == 0 {
			return false
		}
		for _, account := range provider.Accounts {
			if account.Disabled || account.IsUnmonitored {
				continue
			}
			// A row whose presence metadata is older than the stale window and
			// whose readings are equally old is not current negative evidence:
			// it may be a credential CPA no longer has. It stays in the report,
			// but it must not turn an otherwise healthy reading into "data
			// missing" for every account in the channel.
			if !account.HasCurrentPresence(now) {
				continue
			}
			hasAccount = true
			if !hasFreshQuotaReading(account, now) {
				return false
			}
		}
	}
	return hasAccount
}

func hasFreshQuotaReading(account AccountReport, now time.Time) bool {
	if !credentialRecovered(account) || account.CollectionFailed || !observationFresh(account, now, defaultFreshWindow) || len(account.Windows) == 0 {
		return false
	}
	for _, window := range account.Windows {
		if !validPercent(window.UsedPercent) {
			return false
		}
	}
	return true
}

func (s *Service) revalidateNewNotifications(state *State, resolved Resolved, report Report, has bool, now time.Time) {
	var stored *Report
	if has {
		stored = &report
	}
	cfg := PolicyConfig{TightRemainingPercent: resolved.TightRemainingPercent, ChannelUnavailableAfter: resolved.ChannelUnavailableAfter, MonitorDownAfter: resolved.MonitorDownAfter}
	for index := range state.Outbox {
		notification := &state.Outbox[index]
		if notification.CreatedAt.Equal(now) && (notification.State == NotifyPending || notification.State == NotifyQueued) {
			before := *notification
			notification.Alerts = eligibleNotificationAlerts(*notification, stored, state.ChannelTracks, cfg, now)
			if !notificationHasContent(*notification) {
				discardNotification(notification)
				releaseDiscardedClaims(state, before, *notification)
			}
		}
	}
}

func notificationHasContent(notification Notification) bool {
	if notification.Kind == KindQuery || notification.Kind == KindMorning || notification.Kind == KindSummary {
		return true
	}
	for _, alert := range notification.Alerts {
		if alert.Kind != statusHealthy && alert.Kind != statusUnknown && alert.Kind != statusReported && alert.Kind != "omitted" {
			return true
		}
	}
	return false
}

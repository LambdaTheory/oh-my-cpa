package feishu

import "time"

// releaseDiscardedClaims releases only reservations made by this unsuccessful
// delivery. Ownership stays open; a later eligible observation may admit one send.
// Millisecond comparison matches the existing persisted pending timestamps.
func releaseDiscardedClaims(state *State, before, outcome Notification) {
	if state == nil || !before.SentAt.IsZero() || !isDiscardedNotification(outcome) {
		return
	}
	claims := notificationClaimTimes(before)
	for index := range state.PendingItems {
		item := &state.PendingItems[index]
		claimAt, covered := claims[item.ID]
		if !covered || item.Status != PendingOpen || !sameClaimInstant(claimAt, item.NotifiedAt) || hasDeliveredClaim(state, *item) {
			continue
		}
		item.NotifiedAt = time.Time{}
	}
	releaseDiscardedSummarySlot(state, before)
}

func notificationClaimTimes(notification Notification) map[string]time.Time {
	fallback := notification.OccurredAt
	if !isMonitoringTimeValid(fallback) {
		fallback = notification.CreatedAt
	}
	claims := make(map[string]time.Time, len(notification.ItemIDs)+len(notification.Alerts))
	for _, id := range notification.ItemIDs {
		claims[id] = fallback
	}
	for _, alert := range decisionRiskAlerts(notification.Alerts) {
		at := alert.OccurredAt
		if !isMonitoringTimeValid(at) {
			at = fallback
		}
		claims[alertItemID(alert)] = at
	}
	return claims
}

func deliveryClaimKey(id, kind string) string {
	return id + "\x00" + kind
}

// An existing live delivery, including a restarted legacy queue, owns admission
// until it succeeds or is discarded. Historical summary selectors do not claim
// a current risk, and a queued forecast cannot delay an exhaustion escalation.
func pendingDeliveryClaims(outbox []Notification) map[string]time.Time {
	claims := map[string]time.Time{}
	for _, row := range outbox {
		if row.State != NotifyPending && row.State != NotifyQueued {
			continue
		}
		times := notificationClaimTimes(row)
		if isActionableKind(row.Kind) || row.Kind == KindAlertBatch {
			for _, id := range row.ItemIDs {
				claims[deliveryClaimKey(id, row.Kind)] = times[id]
			}
		}
		for _, alert := range decisionRiskAlerts(row.Alerts) {
			id := alertItemID(alert)
			claims[deliveryClaimKey(id, alert.Kind)] = times[id]
		}
	}
	return claims
}

// A legacy row with no reservation clock can still receive a real acknowledgement.
// Only delivered risks may fill that missing claim; diagnostic selectors may not.
func acknowledgeDeliveredClaims(state *State, outcome Notification) {
	if outcome.State != NotifySent || outcome.SentAt.IsZero() {
		return
	}
	covered := map[string]time.Time{}
	for _, alert := range decisionRiskAlerts(outcome.Alerts) {
		at := alert.OccurredAt
		if !isMonitoringTimeValid(at) {
			at = alert.ObservedAt
		}
		covered[alertItemID(alert)] = at
	}
	for index := range state.PendingItems {
		item := &state.PendingItems[index]
		at, exists := covered[item.ID]
		if item.Status == PendingOpen && item.NotifiedAt.IsZero() && exists && isMonitoringTimeValid(at) && !at.Before(item.Since) {
			item.NotifiedAt = outcome.SentAt
		}
	}
}

func sameClaimInstant(left, right time.Time) bool {
	return isMonitoringTimeValid(left) && isMonitoringTimeValid(right) && left.UnixMilli() == right.UnixMilli()
}

// Actual delivery protects its episode even when another queued row overlaps it.
// A newer reservation cannot borrow an older episode's acknowledgement.
func hasDeliveredClaim(state *State, item PendingItem) bool {
	for _, row := range state.Outbox {
		if row.State != NotifySent || row.SentAt.IsZero() || row.SentAt.Before(item.Since) || row.SentAt.UnixMilli() < item.NotifiedAt.UnixMilli() {
			continue
		}
		for _, alert := range decisionRiskAlerts(row.Alerts) {
			if alertItemID(alert) == item.ID {
				return true
			}
		}
		for _, reported := range row.Pending {
			if reported.ID == item.ID && sameClaimInstant(reported.NotifiedAt, item.NotifiedAt) {
				return true
			}
		}
		// Older acknowledged payloads may retain only their selected identities.
		if len(row.Alerts) == 0 && len(row.Pending) == 0 {
			for _, id := range row.ItemIDs {
				if id == item.ID {
					return true
				}
			}
		}
	}
	return false
}

func isMorningNotification(kind string) bool {
	return kind == KindMorning || kind == predictionKindPrefix+KindMorning
}

// LastSummaryAt reserves one scheduled minute. A discarded morning must not
// consume it, but a concurrent pending or actually delivered morning still owns it.
func releaseDiscardedSummarySlot(state *State, before Notification) {
	if !isMorningNotification(before.Kind) || state.LastSummaryAt.IsZero() || before.CreatedAt.IsZero() {
		return
	}
	slot := before.CreatedAt.UTC().Truncate(time.Minute)
	if !state.LastSummaryAt.UTC().Truncate(time.Minute).Equal(slot) {
		return
	}
	for _, row := range state.Outbox {
		if row.ID == before.ID || !isMorningNotification(row.Kind) || isDiscardedNotification(row) || !row.CreatedAt.UTC().Truncate(time.Minute).Equal(slot) {
			continue
		}
		return
	}
	state.LastSummaryAt = time.Time{}
}

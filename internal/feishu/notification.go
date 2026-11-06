package feishu

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// Notification delivery states. A closed row is actually delivered only when
// SentAt is nonzero; an evidence-based discard keeps the existing sent state.
const (
	NotifyPending = "pending"
	NotifyQueued  = "queued"
	NotifySent    = "sent"
)

// Notification is persisted before delivery and contains no credential material.
type Notification struct {
	ID      string
	Kind    string
	ItemIDs []string
	Alerts  []Alert
	Pending []PendingItem
	Tight   []TightChannel
	// Omitted counts findings kept out of Alerts by the payload limit.
	Omitted       int
	State         string
	Attempts      int
	LastError     string
	NextAttemptAt time.Time
	SentAt        time.Time
	OccurredAt    time.Time
	CreatedAt     time.Time
}

type Dispatcher struct {
	channelUnavailableAfter time.Duration
	monitorDownAfter        time.Duration
	source                  ReportSource
	// Prediction delivery re-reads the stored roster, including on every retry.
	predictionSource PredictionSource
	store            Store
	sender           Sender
	renderer         ReportRenderer
	logger           *slog.Logger
	now              func() time.Time
	backoffBase      time.Duration
	backoffMax       time.Duration
}

func NewDispatcher(sender Sender, renderer ReportRenderer, logger *slog.Logger) *Dispatcher {
	if logger == nil {
		logger = slog.Default()
	}
	return &Dispatcher{sender: sender, renderer: renderer, logger: logger, now: time.Now,
		backoffBase: 30 * time.Second, backoffMax: 30 * time.Minute}
}

func (d *Dispatcher) withClock(now func() time.Time) *Dispatcher {
	if now != nil {
		d.now = now
	}
	return d
}

// deliverDue returns delivery progress for persistence. Failed sends remain
// pending; closing an ineligible row does not manufacture a delivery timestamp.
func (d *Dispatcher) deliverDue(ctx context.Context, chatID string, outbox []Notification) []Notification {
	out := append([]Notification(nil), outbox...)
	for index := range out {
		notification := &out[index]
		if notification.State == NotifySent || notification.State == NotifyQueued {
			continue
		}
		if notification.NextAttemptAt.After(d.now()) {
			continue
		}
		notification.State = NotifyPending
		sent, err := d.sendWithFallback(ctx, chatID, notification)
		if err != nil {
			notification.LastError = err.Error()
			notification.Attempts++
			notification.NextAttemptAt = d.now().Add(backoffDelay(notification.Attempts, d.backoffBase, d.backoffMax))
			d.logger.WarnContext(ctx, "feishu notification send failed", "action", "notify", "kind", notification.Kind, "attempts", notification.Attempts, "error", err)
		} else if sent {
			notification.State = NotifySent
			notification.LastError = ""
			notification.NextAttemptAt = time.Time{}
			notification.SentAt = d.now()
		} else {
			discardNotification(notification)
		}
	}
	return out
}

func isDiscardedNotification(notification Notification) bool {
	return notification.State == NotifySent && notification.SentAt.IsZero()
}

func discardNotification(notification *Notification) {
	notification.State = NotifySent
	notification.LastError = ""
	notification.NextAttemptAt = time.Time{}
	notification.SentAt = time.Time{}
}

func (d *Dispatcher) deliveryMessage(ctx context.Context, notification *Notification) (Message, bool, error) {
	if d.predictionSource != nil && strings.HasPrefix(notification.Kind, predictionKindPrefix) {
		alerts, err := d.currentPredictionAlerts(ctx, notification.Alerts)
		if err != nil {
			return Message{}, false, err
		}
		notification.Alerts = alerts
	}
	now := d.now()
	msg := Message{EvidenceChecked: true, ChannelUnavailableAfter: d.channelUnavailableAfter,
		EvaluatedAt: now, Kind: notification.Kind, Pending: notification.Pending,
		Omitted: notification.Omitted, GeneratedAt: notification.OccurredAt}
	if d.source != nil {
		if report, has := d.source.ReadReport(ctx); has {
			msg.Report = &report
		}
	}
	var tracks map[string]ChannelTrack
	if d.store != nil {
		state, err := d.store.Load(ctx)
		if err != nil {
			d.logger.WarnContext(ctx, "feishu delivery evidence state unavailable", "error", err)
		} else {
			tracks = state.ChannelTracks
			if usesCompactCard(msg.Kind) {
				// These items are diagnostic/omission context only. Qualification
				// below uses the notification's selected identities, not this backlog.
				msg.Pending = openItems(state.PendingItems)
				for _, item := range state.PendingItems {
					if item.Kind == predictionKindPrefix && item.ResolvedAt.IsZero() {
						msg.Pending = append(msg.Pending, item)
					}
				}
			}
		}
	}
	cfg := PolicyConfig{ChannelUnavailableAfter: d.channelUnavailableAfter, MonitorDownAfter: d.monitorDownAfter}
	msg.Alerts = eligibleNotificationAlerts(*notification, msg.Report, tracks, cfg, now)
	notification.Alerts = msg.Alerts
	hasPrediction := false
	for _, alert := range msg.Alerts {
		hasPrediction = hasPrediction || alert.Kind == predictionKindPrefix
	}
	if !hasPrediction && strings.HasPrefix(msg.Kind, predictionKindPrefix) {
		msg.Omitted, notification.Omitted = 0, 0
	}
	return msg, notificationHasContent(*notification), nil
}

// The original queued evidence must remain age-bounded even when the current
// roster still contains its identity. A legacy missing source can be recovered
// only when the stored digest proves the same observation, never from queue time.
func (d *Dispatcher) currentPredictionAlerts(ctx context.Context, alerts []Alert) ([]Alert, error) {
	digest, err := d.predictionSource(ctx)
	if err != nil {
		return nil, err
	}
	live := make(map[string]PredictionDigestItem, len(digest.Items))
	for _, item := range predictionFindings(digest.Items, d.now()) {
		live[predictionItemID(item)] = item
	}
	kept := make([]Alert, 0, len(alerts))
	findings := 0
	for _, alert := range alerts {
		if alert.Kind != predictionKindPrefix {
			kept = append(kept, alert)
			continue
		}
		item, exists := live[alertItemID(alert)]
		if !exists {
			continue
		}
		if alert.EvidenceSource == "" && alert.ObservedAt.UnixMilli() == item.ObservedAtMS && alert.FreshWindowMS > 0 {
			alert.EvidenceSource = EvidenceQuota
			if item.FreshWindowMS < alert.FreshWindowMS {
				alert.FreshWindowMS = item.FreshWindowMS
			}
		}
		kept = append(kept, alert)
		findings++
	}
	if findings == 0 {
		withoutFooter := kept[:0]
		for _, alert := range kept {
			if alert.Kind != "omitted" {
				withoutFooter = append(withoutFooter, alert)
			}
		}
		kept = withoutFooter
	}
	return kept, nil
}

// A rejected full-card request may cross a bound or a roster change; the
// simplified fallback must independently re-read and requalify every item.
func (d *Dispatcher) sendWithFallback(ctx context.Context, chatID string, notification *Notification) (bool, error) {
	msg, eligible, err := d.deliveryMessage(ctx, notification)
	if err != nil || !eligible {
		return false, err
	}
	if err := d.sender.SendCard(ctx, chatID, d.renderer.Card(msg)); err == nil {
		return true, nil
	} else {
		d.logger.WarnContext(ctx, "feishu full card rejected, retrying simplified", "action", "notify", "result", "fallback", "error", err)
	}
	msg, eligible, err = d.deliveryMessage(ctx, notification)
	if err != nil || !eligible {
		return false, err
	}
	if err := d.sender.SendCard(ctx, chatID, d.renderer.CardSimple(msg)); err != nil {
		return false, fmt.Errorf("full and simplified card both failed: %w", err)
	}
	d.logger.InfoContext(ctx, "feishu simplified card sent", "action", "notify", "result", "degraded")
	return true, nil
}

func backoffDelay(attempt int, base, max time.Duration) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := base
	for i := 1; i < attempt; i++ {
		delay *= 2
		if delay >= max {
			return max
		}
	}
	if delay > max {
		return max
	}
	return delay
}

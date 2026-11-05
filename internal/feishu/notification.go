package feishu

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// Notification delivery states.
const (
	NotifyPending = "pending" // due to send
	NotifyQueued  = "queued"  // held back by quiet hours
	NotifySent    = "sent"    // delivered; kept briefly as an acknowledgement
)

// Notification is one unit of outbound delivery: a single immediate alert or a
// merged quiet-end/scheduled digest. It is secret-free and is what the outbox
// persists until Feishu confirms delivery. Persisting it before sending, and
// marking it sent only after a successful send, is what makes a failed send
// retried rather than lost.
type Notification struct {
	ID      string
	Kind    string
	ItemIDs []string
	Alerts  []Alert
	// Pending and Tight carry a scheduled or quiet-end digest, which has no single
	// alert. They are empty for an immediate alert notification.
	Pending []PendingItem
	Tight   []TightChannel
	// Omitted counts findings the payload limit kept out of Alerts. The card
	// discloses the count instead of silently dropping rows.
	Omitted   int
	State     string
	Attempts  int
	LastError string
	// NextAttemptAt is when a failed send may be retried.
	NextAttemptAt time.Time
	SentAt        time.Time
	OccurredAt    time.Time
	CreatedAt     time.Time
}

// Dispatcher renders and sends queued notifications, honouring a bounded
// exponential retry. It marks a notification sent only after the sender returns
// nil; a failure leaves it pending with a later NextAttemptAt.
type Dispatcher struct {
	channelUnavailableAfter time.Duration
	source                  ReportSource
	// predictionSource re-reads the prediction findings for a queued card. Quiet
	// hours and the bounded retry can hold a notification for hours, so the
	// roster and the reading freshness are checked again right before sending
	// rather than trusting the state the card was queued with. Nil means the
	// dispatcher was built without a prediction job and prediction cards are
	// delivered as queued.
	predictionSource PredictionSource
	store            Store
	sender           Sender
	renderer         ReportRenderer
	logger           *slog.Logger
	now              func() time.Time
	backoffBase      time.Duration
	backoffMax       time.Duration
}

// NewDispatcher builds a dispatcher. A nil logger and nil clock fall back to the
// defaults so a test can construct one with only the seams it cares about.
func NewDispatcher(sender Sender, renderer ReportRenderer, logger *slog.Logger) *Dispatcher {
	if logger == nil {
		logger = slog.Default()
	}
	return &Dispatcher{
		sender:      sender,
		renderer:    renderer,
		logger:      logger,
		now:         time.Now,
		backoffBase: 30 * time.Second,
		backoffMax:  30 * time.Minute,
	}
}

// withClock overrides the dispatcher clock. Tests use it to drive the retry
// schedule without waiting.
func (d *Dispatcher) withClock(now func() time.Time) *Dispatcher {
	if now != nil {
		d.now = now
	}
	return d
}

// deliverDue attempts every pending notification whose retry time has passed and
// returns the outbox with the outcomes applied. The chatID is the configured
// target group. The returned slice contains sent rows as well, so the caller can
// persist the acknowledgement and prune it later; it never contains an alert
// whose send failed, which stays pending.
func (d *Dispatcher) deliverDue(ctx context.Context, chatID string, outbox []Notification) []Notification {
	now := d.now()
	out := make([]Notification, len(outbox))
	copy(out, outbox)
	for i := range out {
		notification := out[i]
		if notification.State == NotifySent || notification.State == NotifyQueued {
			continue
		}
		if notification.State != NotifyPending {
			notification.State = NotifyPending
		}
		if notification.NextAttemptAt.After(now) {
			out[i] = notification
			continue
		}
		if d.predictionSource != nil && strings.HasPrefix(notification.Kind, predictionKindPrefix) {
			alerts, err := d.currentPredictionAlerts(ctx, notification.Alerts)
			if err != nil {
				notification.LastError = err.Error()
				notification.Attempts++
				notification.NextAttemptAt = now.Add(backoffDelay(notification.Attempts, d.backoffBase, d.backoffMax))
				d.logger.WarnContext(ctx, "feishu prediction digest re-read failed",
					"action", "notify", "kind", notification.Kind, "attempts", notification.Attempts, "error", err)
				out[i] = notification
				continue
			}
			if len(alerts) == 0 && len(notification.Pending) == 0 && len(notification.Tight) == 0 {
				// Nothing the queued card claimed is still true: the reading went
				// stale, the credential was disabled or removed, or the window now
				// lasts to its reset. Sending nothing is the whole outcome; the
				// row is marked sent so the outbox stops retrying it.
				d.logger.InfoContext(ctx, "feishu prediction card has no current finding",
					"action", "notify", "kind", notification.Kind, "result", "dropped")
				notification.Alerts = alerts
				notification.State = NotifySent
				notification.LastError = ""
				notification.SentAt = now
				out[i] = notification
				continue
			}
			notification.Alerts = alerts
		}
		msg := Message{
			ChannelUnavailableAfter: d.channelUnavailableAfter,
			EvaluatedAt:             now,
			Kind:                    notification.Kind,
			Alerts:                  notification.Alerts,
			Pending:                 notification.Pending,
			Tight:                   notification.Tight,
			Omitted:                 notification.Omitted,
			GeneratedAt:             notification.OccurredAt,
		}
		if d.source != nil && usesCompactCard(msg.Kind) {
			if report, has := d.source.ReadReport(ctx); has {
				msg.Report = &report
			}
		}
		if d.store != nil && usesCompactCard(msg.Kind) {
			if state, err := d.store.Load(ctx); err == nil {
				msg.Pending = openItems(state.PendingItems)
				for _, item := range state.PendingItems {
					if item.Kind == predictionKindPrefix && item.ResolvedAt.IsZero() {
						msg.Pending = append(msg.Pending, item)
					}
				}
			}
		}
		if strings.HasPrefix(msg.Kind, predictionKindPrefix) {
			// Freshness is judged again here, not just when the finding was queued:
			// quiet hours and retry backoff can hold a card for hours, and a reading
			// that has aged out is no longer evidence.
			fresh := make([]Alert, 0, len(msg.Alerts))
			for _, alert := range msg.Alerts {
				if !alertStale(alert, now) {
					fresh = append(fresh, alert)
				}
			}
			msg.Alerts = fresh
			if len(msg.Alerts) == 0 && len(msg.Pending) == 0 && len(msg.Tight) == 0 {
				// Nothing evidence-based survived. Sending a stale conclusion (or an
				// "expired" notice with no action) only adds noise, so the card is
				// dropped and acknowledged rather than retried forever.
				notification.State = NotifySent
				notification.LastError = ""
				notification.SentAt = now
				d.logger.InfoContext(ctx, "feishu prediction card dropped",
					"action", "notify", "result", "stale_reading", "kind", notification.Kind)
				out[i] = notification
				continue
			}
		}
		if err := d.sendWithFallback(ctx, chatID, msg); err != nil {
			notification.LastError = err.Error()
			notification.Attempts++
			notification.NextAttemptAt = now.Add(backoffDelay(notification.Attempts, d.backoffBase, d.backoffMax))
			d.logger.WarnContext(ctx, "feishu notification send failed",
				"action", "notify", "kind", notification.Kind, "attempts", notification.Attempts, "error", err)
			out[i] = notification
			continue
		}
		notification.State = NotifySent
		notification.LastError = ""
		notification.SentAt = now
		out[i] = notification
	}
	return out
}

// currentPredictionAlerts keeps only the alerts a fresh digest still supports.
// The digest is read by the operations layer, so a credential that has since
// been disabled, deleted, left without a window or gone stale is already absent
// from it; this is the "check the roster immediately before sending" step, run
// on every delivery attempt rather than only when the card is queued. Alerts
// that are not prediction rows (a morning digest can carry report alerts in the
// same notification) are kept as they are.
func (d *Dispatcher) currentPredictionAlerts(ctx context.Context, alerts []Alert) ([]Alert, error) {
	digest, err := d.predictionSource(ctx)
	if err != nil {
		return nil, err
	}
	live := make(map[string]bool, len(digest.Items))
	for _, item := range predictionFindings(digest.Items, d.now()) {
		live[predictionItemID(item)] = true
	}
	kept := make([]Alert, 0, len(alerts))
	for _, alert := range alerts {
		if alert.Kind == predictionKindPrefix && !live[alertItemID(alert)] {
			continue
		}
		kept = append(kept, alert)
	}
	return kept, nil
}

// sendWithFallback posts the full card and, on failure, the simplified card. It
// returns the variant that succeeded ("full" or "simple") in the audit log.
func (d *Dispatcher) sendWithFallback(ctx context.Context, chatID string, msg Message) error {
	if err := d.sender.SendCard(ctx, chatID, d.renderer.Card(msg)); err == nil {
		return nil
	} else {
		d.logger.WarnContext(ctx, "feishu full card rejected, retrying simplified",
			"action", "notify", "result", "fallback", "error", err)
	}
	if err := d.sender.SendCard(ctx, chatID, d.renderer.CardSimple(msg)); err != nil {
		return fmt.Errorf("full and simplified card both failed: %w", err)
	}
	d.logger.InfoContext(ctx, "feishu simplified card sent", "action", "notify", "result", "degraded")
	return nil
}

// backoffDelay is the bounded exponential retry for a failed send.
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

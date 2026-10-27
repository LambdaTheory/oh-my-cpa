package feishu

import (
	"context"
	"fmt"
	"log/slog"
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
	Pending  []PendingItem
	Tight    []TightChannel
	State    string
	Attempts int
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
	sender      Sender
	renderer    ReportRenderer
	logger      *slog.Logger
	now         func() time.Time
	backoffBase time.Duration
	backoffMax  time.Duration
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
		msg := Message{
			EvaluatedAt: now,
			Kind:        notification.Kind,
			Alerts:      notification.Alerts,
			Pending:     notification.Pending,
			Tight:       notification.Tight,
			GeneratedAt: notification.OccurredAt,
		}
		if err := d.sendWithFallback(ctx, chatID, msg); err != nil {
			notification.Attempts++
			notification.NextAttemptAt = now.Add(backoffDelay(notification.Attempts, d.backoffBase, d.backoffMax))
			d.logger.WarnContext(ctx, "feishu notification send failed",
				"action", "notify", "kind", notification.Kind, "attempts", notification.Attempts, "error", err)
			out[i] = notification
			continue
		}
		notification.State = NotifySent
		notification.SentAt = now
		out[i] = notification
	}
	return out
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

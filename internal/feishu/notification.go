package feishu

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Notification delivery states.
const (
	NotifyPending    = "pending"    // due to send
	NotifyQueued     = "queued"     // held back by quiet hours
	NotifySent       = "sent"       // delivered; kept briefly as an acknowledgement
	NotifySuppressed = "suppressed" // no eligible evidence; never acknowledged as sent
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
	Pending   []PendingItem
	Tight     []TightChannel
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
	monitorDownAfter        time.Duration
	source                  ReportSource
	store                   Store
	sender                  Sender
	renderer                ReportRenderer
	logger                  *slog.Logger
	now                     func() time.Time
	backoffBase             time.Duration
	backoffMax              time.Duration
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
// persist acknowledgements or terminal suppression and prune them later. Failed
// sends remain in the returned slice as pending rows with a retry deadline.
func (d *Dispatcher) deliverDue(ctx context.Context, chatID string, outbox []Notification) []Notification {
	out := append([]Notification(nil), outbox...)
	for index := range out {
		notification := &out[index]
		if notification.State == NotifySent || notification.State == NotifyQueued || notification.State == NotifySuppressed {
			continue
		}
		now := d.now()
		if notification.NextAttemptAt.After(now) {
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
			suppressNotification(notification)
		}
	}
	return out
}

func suppressNotification(notification *Notification) {
	notification.State = NotifySuppressed
	notification.LastError = ""
	notification.NextAttemptAt = time.Time{}
}

func (d *Dispatcher) deliveryMessage(ctx context.Context, notification *Notification) (Message, bool) {
	now := d.now()
	msg := Message{EvidenceChecked: true, ChannelUnavailableAfter: d.channelUnavailableAfter,
		EvaluatedAt: now, Kind: notification.Kind, Pending: notification.Pending, GeneratedAt: notification.OccurredAt}
	// Every path reads only stored projections. Aggregate report time cannot renew
	// a forecast or a retained authentication event.
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
		}
	}
	cfg := PolicyConfig{ChannelUnavailableAfter: d.channelUnavailableAfter, MonitorDownAfter: d.monitorDownAfter}
	msg.Alerts = eligibleNotificationAlerts(*notification, msg.Report, tracks, cfg, now)
	notification.Alerts = msg.Alerts
	return msg, notificationHasContent(*notification)
}

// A full-card request may itself cross the evidence bound, so the degraded send
// must rebuild its evidence independently rather than reuse the first view model.
func (d *Dispatcher) sendWithFallback(ctx context.Context, chatID string, notification *Notification) (bool, error) {
	msg, eligible := d.deliveryMessage(ctx, notification)
	if !eligible {
		return false, nil
	}
	if err := d.sender.SendCard(ctx, chatID, d.renderer.Card(msg)); err == nil {
		return true, nil
	} else {
		d.logger.WarnContext(ctx, "feishu full card rejected, retrying simplified", "action", "notify", "result", "fallback", "error", err)
	}
	msg, eligible = d.deliveryMessage(ctx, notification)
	if !eligible {
		return false, nil
	}
	if err := d.sender.SendCard(ctx, chatID, d.renderer.CardSimple(msg)); err != nil {
		return false, fmt.Errorf("full and simplified card both failed: %w", err)
	}
	d.logger.InfoContext(ctx, "feishu simplified card sent", "action", "notify", "result", "degraded")
	return true, nil
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

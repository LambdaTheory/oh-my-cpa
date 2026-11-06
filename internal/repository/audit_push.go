package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// AuditPushEvent is the platform copy of one audit record. It is written in the same
// transaction as the local audit_events row, so "the local record exists" and "the
// platform will be told" cannot diverge (PRD 4.8).
type AuditPushEvent struct {
	// Payload is the exact JSON sent to the audit platform. Retry replays these bytes,
	// never a rebuilt event, so the timestamp, trace id and business fields cannot
	// change between attempts.
	Payload  []byte
	TraceID  string
	SpanID   string
	ToolName string
	Event    string
}

// WithTraceID re-points an event at the row that actually holds the flow. It exists
// for the one path where an identical pending application is refreshed instead of
// created: the caller generated an id for a row that was never inserted, and the trace
// the platform joins steps by must be the existing application's.
func (p AuditPushEvent) WithTraceID(traceID string) *AuditPushEvent {
	traceID = strings.TrimSpace(traceID)
	if traceID == "" || traceID == p.TraceID {
		return &p
	}
	var payload map[string]any
	if err := json.Unmarshal(p.Payload, &payload); err != nil {
		return &p
	}
	payload["trace_id"] = traceID
	encoded, err := json.Marshal(payload)
	if err != nil {
		return &p
	}
	p.TraceID = traceID
	p.Payload = encoded
	return &p
}

// QueuedAuditPush is one pending platform event as the pusher sees it.
type QueuedAuditPush struct {
	ID           int64
	AuditEventID int64
	CreatedAtMS  int64
	TraceID      string
	SpanID       string
	ToolName     string
	Event        string
	Payload      []byte
	Attempts     int
}

// AuditPushStatus answers "is the platform actually receiving these" without
// pretending the console's own records are enough (PRD 4.8, AC-32).
type AuditPushStatus struct {
	Pending       int64
	EarliestMS    int64
	LastSuccessMS int64
	Quarantined   int64
}

func insertAuditPush(ctx context.Context, execer auditExecer, auditEventID int64, push AuditPushEvent) error {
	if len(push.Payload) == 0 {
		return errors.New("audit push payload is required")
	}
	if event := push.Event; event != "tool.end" && event != "tool.error" {
		return fmt.Errorf("audit push event %q is not tool.end or tool.error", event)
	}
	_, err := execer.ExecContext(ctx, `
		INSERT INTO audit_push_events (
			audit_event_id, created_at_ms, trace_id, span_id, tool_name, event, payload_json
		) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		auditEventID, time.Now().UnixMilli(), push.TraceID, push.SpanID, push.ToolName, push.Event, string(push.Payload),
	)
	if err != nil {
		return fmt.Errorf("insert audit push event: %w", err)
	}
	return nil
}

// ListPendingAuditPushes returns the oldest events that are due, in insertion order.
// The pusher stops at the first event it cannot deliver, so ordering here is what
// keeps a reconnect replay in the order the actions happened (AC-37).
func (r *Repository) ListPendingAuditPushes(ctx context.Context, nowMS int64, limit int) ([]QueuedAuditPush, error) {
	if r == nil || r.SQL() == nil {
		return nil, errors.New("repository is not initialized")
	}
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := r.SQL().QueryContext(ctx, `
		SELECT id, audit_event_id, created_at_ms, trace_id, span_id, tool_name, event, payload_json, attempts
		FROM audit_push_events
		WHERE state = 'pending' AND next_attempt_ms <= ?
		ORDER BY id
		LIMIT ?`, nowMS, limit)
	if err != nil {
		return nil, fmt.Errorf("list pending audit pushes: %w", err)
	}
	defer rows.Close()

	events := make([]QueuedAuditPush, 0, limit)
	for rows.Next() {
		var event QueuedAuditPush
		var payload string
		if err := rows.Scan(&event.ID, &event.AuditEventID, &event.CreatedAtMS, &event.TraceID,
			&event.SpanID, &event.ToolName, &event.Event, &payload, &event.Attempts); err != nil {
			return nil, fmt.Errorf("scan pending audit push: %w", err)
		}
		event.Payload = []byte(payload)
		events = append(events, event)
	}
	return events, rows.Err()
}

// MarkAuditPushesSent records delivery. It is a separate statement from the request
// because the platform confirms per event: a batch can be partly accepted.
func (r *Repository) MarkAuditPushesSent(ctx context.Context, ids []int64, nowMS int64) error {
	if len(ids) == 0 {
		return nil
	}
	if err := r.execAuditPushIDs(ctx, ids, `
		UPDATE audit_push_events
		SET state = 'sent', sent_at_ms = ?, last_attempt_ms = ?, last_error = ''
		WHERE id = ? AND state = 'pending'`, nowMS, nowMS); err != nil {
		return err
	}
	return nil
}

// RescheduleAuditPushes keeps unconfirmed events for a later attempt with the original
// payload and adds only attempt bookkeeping.
func (r *Repository) RescheduleAuditPushes(ctx context.Context, ids []int64, nowMS, nextAttemptMS int64, reason string) error {
	if len(ids) == 0 {
		return nil
	}
	if err := r.execAuditPushIDs(ctx, ids, `
		UPDATE audit_push_events
		SET attempts = attempts + 1, last_attempt_ms = ?, next_attempt_ms = ?, last_error = ?
		WHERE id = ? AND state = 'pending'`, nowMS, nextAttemptMS, truncateError(reason)); err != nil {
		return err
	}
	return nil
}

// QuarantineAuditPushes removes events from the replay queue after a rejection the
// contract says not to retry blindly (400/413/415). They stay visible in the status
// count and keep their payload, so the fix can be diagnosed rather than guessed.
func (r *Repository) QuarantineAuditPushes(ctx context.Context, ids []int64, nowMS int64, reason string) error {
	if len(ids) == 0 {
		return nil
	}
	if err := r.execAuditPushIDs(ctx, ids, `
		UPDATE audit_push_events
		SET state = 'quarantined', attempts = attempts + 1, last_attempt_ms = ?, last_error = ?
		WHERE id = ? AND state = 'pending'`, nowMS, truncateError(reason)); err != nil {
		return err
	}
	return nil
}

func (r *Repository) execAuditPushIDs(ctx context.Context, ids []int64, statement string, prefix ...any) error {
	tx, err := r.SQL().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin audit push update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, id := range ids {
		args := append(append([]any{}, prefix...), id)
		if _, err := tx.ExecContext(ctx, statement, args...); err != nil {
			return fmt.Errorf("update audit push %d: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit audit push update: %w", err)
	}
	return nil
}

// AuditPushStatusOf reads the queue's health for monitoring and logs. "Last success"
// means the most recent event the platform confirmed, not the most recent attempt.
func (r *Repository) AuditPushStatusOf(ctx context.Context) (AuditPushStatus, error) {
	if r == nil || r.SQL() == nil {
		return AuditPushStatus{}, errors.New("repository is not initialized")
	}
	var status AuditPushStatus
	var earliest, lastSuccess sql.NullInt64
	err := r.SQL().QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN state = 'pending' THEN 1 ELSE 0 END), 0),
			MIN(CASE WHEN state = 'pending' THEN created_at_ms END),
			MAX(CASE WHEN state = 'sent' THEN sent_at_ms END),
			COALESCE(SUM(CASE WHEN state = 'quarantined' THEN 1 ELSE 0 END), 0)
		FROM audit_push_events`).Scan(&status.Pending, &earliest, &lastSuccess, &status.Quarantined)
	if err != nil {
		return AuditPushStatus{}, fmt.Errorf("read audit push status: %w", err)
	}
	status.EarliestMS = earliest.Int64
	status.LastSuccessMS = lastSuccess.Int64
	return status, nil
}

// truncateError keeps the stored failure note readable and bounded; the platform's
// response body could otherwise be arbitrarily long.
func truncateError(value string) string {
	runes := []rune(value)
	if len(runes) > 500 {
		return string(runes[:500])
	}
	return string(runes)
}

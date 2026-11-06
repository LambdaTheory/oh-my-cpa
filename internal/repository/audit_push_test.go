package repository

import (
	"context"
	"strings"
	"testing"
)

// TestAuditPushIsQueuedWithItsAuditRecord asserts the outbox row and the audit row are
// written by one transaction: the platform is owed the event exactly when the console
// recorded it (PRD 4.8).
func TestAuditPushIsQueuedWithItsAuditRecord(t *testing.T) {
	repo, _ := testRepository(t)
	ctx := context.Background()

	payload := `{"ts":"2026-11-05T10:00:00.000Z","agent_id":"oh-my-cpa","trace_id":"app-1","span_id":"span-1","event":"tool.end","tool_name":"repo_key.apply","status":"OK","result_summary":"applied"}`
	id, err := repo.RecordAuditEvent(ctx, AuditEvent{
		Action: "repo_key.apply", TargetType: "repo_api_key", TargetID: "org/repo",
		Result: "success",
		Push: &AuditPushEvent{
			Payload: []byte(payload), TraceID: "app-1", SpanID: "span-1",
			ToolName: "repo_key.apply", Event: "tool.end",
		},
	})
	if err != nil {
		t.Fatalf("record audit event: %v", err)
	}

	var auditEventID int64
	var stored string
	if err := repo.SQL().QueryRowContext(ctx,
		`SELECT audit_event_id, payload_json FROM audit_push_events WHERE state = 'pending'`).Scan(&auditEventID, &stored); err != nil {
		t.Fatalf("read queued push: %v", err)
	}
	if auditEventID != id {
		t.Fatalf("queued push audit_event_id = %d, want %d", auditEventID, id)
	}
	if stored != payload {
		t.Fatalf("queued payload changed:\n got %s\nwant %s", stored, payload)
	}
}

// TestAuditPushFailureRollsBackTheAuditRecord is the other half of the same promise: an
// event that could not be queued is not silently recorded as if the platform would learn
// about it. The caller sees an error instead.
func TestAuditPushFailureRollsBackTheAuditRecord(t *testing.T) {
	repo, _ := testRepository(t)
	ctx := context.Background()

	_, err := repo.RecordAuditEvent(ctx, AuditEvent{
		Action: "repo_key.stop", TargetType: "repo_api_key", TargetID: "org/repo",
		Push: &AuditPushEvent{TraceID: "binding-1", SpanID: "span-1", ToolName: "repo_key.stop", Event: "tool.end"},
	})
	if err == nil {
		t.Fatal("expected an empty payload to be refused")
	}
	var count int
	if err := repo.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("audit_events count = %d, want 0 after a failed queue write", count)
	}
}

// TestAuditPushReplayKeepsOrderAndPayload asserts the retry path stores byte-identical
// payloads and returns them oldest first, which is what lets a reconnect replay in the
// order the actions happened (AC-37).
func TestAuditPushReplayKeepsOrderAndPayload(t *testing.T) {
	repo, _ := testRepository(t)
	ctx := context.Background()

	for _, trace := range []string{"app-1", "app-2"} {
		payload := `{"ts":"2026-11-05T10:00:00.000Z","trace_id":"` + trace + `"}`
		if _, err := repo.RecordAuditEvent(ctx, AuditEvent{
			Action: "repo_key.apply", TargetType: "repo_api_key", TargetID: "org/repo",
			Push: &AuditPushEvent{Payload: []byte(payload), TraceID: trace, SpanID: "s-" + trace, ToolName: "repo_key.apply", Event: "tool.end"},
		}); err != nil {
			t.Fatalf("record %s: %v", trace, err)
		}
	}

	queued, err := repo.ListPendingAuditPushes(ctx, 2_000_000_000_000, 10)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(queued) != 2 || queued[0].TraceID != "app-1" || queued[1].TraceID != "app-2" {
		t.Fatalf("pending order = %+v, want app-1 then app-2", queued)
	}
	if err := repo.RescheduleAuditPushes(ctx, []int64{queued[0].ID}, 1, 2_000_000_000_001, "timeout"); err != nil {
		t.Fatalf("reschedule: %v", err)
	}
	requeued, err := repo.ListPendingAuditPushes(ctx, 2_000_000_000_000, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(requeued) != 1 || requeued[0].TraceID != "app-2" {
		t.Fatalf("after rescheduling, due events = %+v, want only app-2", requeued)
	}
	if requeued[0].Attempts != 0 {
		t.Fatalf("app-2 attempts = %d, want 0", requeued[0].Attempts)
	}

	status, err := repo.AuditPushStatusOf(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Pending != 2 {
		t.Fatalf("pending count = %d, want 2", status.Pending)
	}
	if err := repo.MarkAuditPushesSent(ctx, []int64{queued[0].ID}, 3); err != nil {
		t.Fatal(err)
	}
	status, err = repo.AuditPushStatusOf(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Pending != 1 || status.LastSuccessMS != 3 {
		t.Fatalf("status = %+v, want one pending and last success 3", status)
	}
}

// TestAuditPushTraceOverrideRepointsAPendingApplication covers the dedupe path: the
// caller generated an id for a row that was not inserted, and the platform trace must
// name the application that exists.
func TestAuditPushTraceOverrideRepointsAPendingApplication(t *testing.T) {
	push := AuditPushEvent{
		Payload:  []byte(`{"trace_id":"generated","tool_name":"repo_key.apply"}`),
		TraceID:  "generated",
		ToolName: "repo_key.apply",
		Event:    "tool.end",
	}
	repointed := push.WithTraceID("existing")
	if repointed.TraceID != "existing" {
		t.Fatalf("trace id = %q, want existing", repointed.TraceID)
	}
	if !strings.Contains(string(repointed.Payload), `"trace_id":"existing"`) {
		t.Fatalf("payload was not repointed: %s", repointed.Payload)
	}
	if push.TraceID != "generated" {
		t.Fatal("the original push must not be mutated")
	}
}

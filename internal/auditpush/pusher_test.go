package auditpush

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/crypto"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

type recordedRequest struct {
	body []byte
}

// newTestRepository opens a fresh database per test; the shared in-memory DSN would let
// one test's queue count another's rows.
func newTestRepository(t *testing.T) *repository.Repository {
	t.Helper()
	sealer, err := crypto.New("01234567890123456789012345678901")
	if err != nil {
		t.Fatal(err)
	}
	database, err := repository.Open(context.Background(), filepath.Join(t.TempDir(), "audit-push.db"), repository.WithCipher(sealer))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return repository.New(database)
}

func enqueue(t *testing.T, repo *repository.Repository, trace string) {
	t.Helper()
	event, payload, err := Build(Description{
		Action: "repo_key.apply", TraceID: trace, RepoID: "org/repo",
		Summary: "applied for org/repo", Channel: ChannelMCP,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RecordAuditEvent(context.Background(), repository.AuditEvent{
		Action: "repo_key.apply", TargetType: "repo_api_key", TargetID: "org/repo",
		Push: &repository.AuditPushEvent{
			Payload: payload, TraceID: event.TraceID, SpanID: event.SpanID,
			ToolName: event.ToolName, Event: event.Event,
		},
	}); err != nil {
		t.Fatal(err)
	}
}

func newTestPusher(t *testing.T, repo *repository.Repository, endpoint string) *Pusher {
	t.Helper()
	pusher, err := NewPusher(repo, Config{URL: endpoint, Timeout: time.Second, RetryEnabled: true, MaxBatch: 10},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return pusher
}

// TestPusherSendsQueuedEvents asserts the happy path: the stored payload is posted as
// the batch's single element, an accepted count clears the queue, and status reports a
// success time (AC-32).
func TestPusherSendsQueuedEvents(t *testing.T) {
	repo := newTestRepository(t)
	enqueue(t, repo, "app-1")
	enqueue(t, repo, "app-2")

	var requests []recordedRequest
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		requests = append(requests, recordedRequest{body: body})
		writer.WriteHeader(http.StatusAccepted)
		_, _ = writer.Write([]byte(`{"accepted":2,"rejected":0,"errors":[]}`))
	}))
	defer server.Close()

	pusher := newTestPusher(t, repo, server.URL)
	if err := pusher.Flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if len(requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(requests))
	}
	var batch struct {
		Events []Event `json:"events"`
	}
	if err := json.Unmarshal(requests[0].body, &batch); err != nil {
		t.Fatalf("decode batch: %v", err)
	}
	if len(batch.Events) != 2 || batch.Events[0].TraceID != "app-1" || batch.Events[1].TraceID != "app-2" {
		t.Fatalf("batch order = %+v", batch.Events)
	}
	status, err := pusher.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Pending != 0 || status.LastSuccessMS == 0 {
		t.Fatalf("status = %+v, want nothing pending and a success time", status)
	}
}

// TestPusherPartialRejectionMarksOnlyConfirmedEvents covers the platform's 202: the
// request was processed, but only the events absent from errors[].index were accepted.
func TestPusherPartialRejectionMarksOnlyConfirmedEvents(t *testing.T) {
	repo := newTestRepository(t)
	enqueue(t, repo, "app-1")
	enqueue(t, repo, "app-2")

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusAccepted)
		_, _ = writer.Write([]byte(`{"accepted":1,"rejected":1,"errors":[{"index":1,"message":"result_summary too long"}]}`))
	}))
	defer server.Close()

	pusher := newTestPusher(t, repo, server.URL)
	if err := pusher.Flush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	status, err := pusher.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Pending != 0 || status.Quarantined != 1 {
		t.Fatalf("status = %+v, want one quarantined and none pending", status)
	}
	var lastError string
	if err := repo.SQL().QueryRowContext(context.Background(),
		`SELECT last_error FROM audit_push_events WHERE trace_id = 'app-2'`).Scan(&lastError); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(lastError, "result_summary too long") {
		t.Fatalf("quarantined reason = %q", lastError)
	}
}

// TestPusherUnreachablePlatformKeepsEveryEvent is AC-37: the platform being down
// changes nothing about the queued events, and no business result depends on this.
func TestPusherUnreachablePlatformKeepsEveryEvent(t *testing.T) {
	repo := newTestRepository(t)
	enqueue(t, repo, "app-1")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {}))
	endpoint := server.URL
	server.Close() // nothing is listening now

	pusher := newTestPusher(t, repo, endpoint)
	if err := pusher.Flush(context.Background()); err == nil {
		t.Fatal("expected a failure against an unreachable platform")
	}
	status, err := pusher.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Pending != 1 {
		t.Fatalf("pending = %d, want the event to stay queued", status.Pending)
	}
	var attempts int
	var nextAttempt int64
	var payload string
	if err := repo.SQL().QueryRowContext(context.Background(),
		`SELECT attempts, next_attempt_ms, payload_json FROM audit_push_events`).Scan(&attempts, &nextAttempt, &payload); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || nextAttempt == 0 {
		t.Fatalf("attempts/next = %d/%d, want one attempt and a scheduled retry", attempts, nextAttempt)
	}
	if !strings.Contains(payload, `"trace_id":"app-1"`) {
		t.Fatalf("payload changed: %s", payload)
	}
}

// TestPusherQuarantinesPayloadRejections covers the contract's 400/413/415 rule: these
// are the caller's bug, so replaying the same bytes is pointless, but the event must
// stay visible rather than vanish.
func TestPusherQuarantinesPayloadRejections(t *testing.T) {
	repo := newTestRepository(t)
	enqueue(t, repo, "app-1")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte(`{"error":"ts is not ISO 8601"}`))
	}))
	defer server.Close()

	pusher := newTestPusher(t, repo, server.URL)
	if err := pusher.Flush(context.Background()); err == nil {
		t.Fatal("expected the flush to report the rejection")
	}
	status, err := pusher.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Pending != 0 || status.Quarantined != 1 {
		t.Fatalf("status = %+v, want the event quarantined rather than retried forever", status)
	}
}

// TestPusherRetryDisabledQuarantinesFailures keeps the status honest when a deployment
// turned replay off: events that will not be retried are not reported as pending.
func TestPusherRetryDisabledQuarantinesFailures(t *testing.T) {
	repo := newTestRepository(t)
	enqueue(t, repo, "app-1")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {}))
	endpoint := server.URL
	server.Close()

	pusher, err := NewPusher(repo, Config{URL: endpoint, Timeout: time.Second, RetryEnabled: false, MaxBatch: 10},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err := pusher.Flush(context.Background()); err == nil {
		t.Fatal("expected a failure")
	}
	status, err := pusher.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Pending != 0 || status.Quarantined != 1 {
		t.Fatalf("status = %+v, want one quarantined", status)
	}
}

// TestNewPusherRejectsUnusableConfiguration stops a typo from becoming a loop that
// silently never sends.
func TestNewPusherRejectsUnusableConfiguration(t *testing.T) {
	repo := newTestRepository(t)
	for _, endpoint := range []string{"", "ftp://example.invalid/ingest", "not a url", "/v1/ingest"} {
		if _, err := NewPusher(repo, Config{URL: endpoint}, nil); err == nil {
			t.Fatalf("endpoint %q should have been refused", endpoint)
		}
	}
}

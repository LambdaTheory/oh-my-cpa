package auditpush

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

// Defaults from the platform's integration guide. They are deliberately conservative:
// the pusher runs beside request handling and must not hold a transaction or a worker
// for long.
const (
	DefaultTimeout  = 1500 * time.Millisecond
	DefaultMaxBatch = 50
	// DefaultInterval is how often the pusher checks for events that are due. Nothing
	// in the request path waits for it (PRD 4.8).
	DefaultInterval = 10 * time.Second
	// maxBackoff caps a retry delay. The first delay is 15 seconds and doubles, so a
	// platform that is down for a day is retried about once every half hour rather
	// than hammered.
	baseBackoff = 15 * time.Second
	maxBackoff  = 30 * time.Minute
)

// Config is the pusher's share of the deployment configuration.
type Config struct {
	// URL is the platform's ingest endpoint. Empty disables the whole feature: the
	// local audit trail is still written, exactly as the guide requires for an agent
	// that has no platform yet.
	URL string
	// Timeout bounds one HTTP request.
	Timeout time.Duration
	// RetryEnabled replays failed events. When false, a failure is quarantined with
	// its reason instead of being retried.
	RetryEnabled bool
	// MaxBatch is the largest number of events sent in one request.
	MaxBatch int
}

// Pusher drains the outbox into the team audit platform.
type Pusher struct {
	repository *repository.Repository
	logger     *slog.Logger
	endpoint   string
	client     *http.Client
	retry      bool
	maxBatch   int
	interval   time.Duration
	// now is injectable so backoff and due-time behavior are testable without sleeping.
	now func() time.Time
}

// NewPusher validates the endpoint and returns a pusher. A malformed URL is a startup
// error rather than a loop that silently never sends anything.
func NewPusher(repository *repository.Repository, config Config, logger *slog.Logger) (*Pusher, error) {
	if repository == nil {
		return nil, errors.New("audit pusher needs a repository")
	}
	endpoint := strings.TrimSpace(config.URL)
	if endpoint == "" {
		return nil, errors.New("audit pusher needs an ingest URL")
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("audit ingest URL %q is not an http(s) URL", endpoint)
	}
	timeout := config.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	maxBatch := config.MaxBatch
	if maxBatch <= 0 || maxBatch > 500 {
		maxBatch = DefaultMaxBatch
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Pusher{
		repository: repository,
		logger:     logger,
		endpoint:   endpoint,
		client:     &http.Client{Timeout: timeout},
		retry:      config.RetryEnabled,
		maxBatch:   maxBatch,
		interval:   DefaultInterval,
		now:        time.Now,
	}, nil
}

// Run drains the queue until the context ends. Losing the platform never stops the
// process: the loop logs and keeps the events (AC-37).
func (p *Pusher) Run(ctx context.Context) error {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		p.pump(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// Flush sends everything that is due, up to a bounded number of batches, and is safe
// to call on shutdown after Run has returned.
func (p *Pusher) Flush(ctx context.Context) error {
	for range 20 {
		sent, err := p.sendOneBatch(ctx)
		if err != nil {
			return err
		}
		if sent == 0 {
			return nil
		}
	}
	return nil
}

// pump sends due batches until one fails or there is nothing left. It is one pass, so
// the ticker controls the cadence and a failing platform cannot spin the loop.
func (p *Pusher) pump(ctx context.Context) {
	for {
		sent, err := p.sendOneBatch(ctx)
		if err != nil {
			p.logger.Warn("audit push failed", "error", err)
			p.reportStatus(ctx)
			return
		}
		if sent == 0 {
			p.reportStatus(ctx)
			return
		}
	}
}

// Status exposes the queue's health for logs and for tests asserting that a backlog is
// visible rather than silent (AC-32).
func (p *Pusher) Status(ctx context.Context) (repository.AuditPushStatus, error) {
	return p.repository.AuditPushStatusOf(ctx)
}

func (p *Pusher) reportStatus(ctx context.Context) {
	status, err := p.Status(ctx)
	if err != nil {
		p.logger.Warn("audit push status unavailable", "error", err)
		return
	}
	if status.Pending == 0 && status.Quarantined == 0 {
		return
	}
	earliest := ""
	if status.EarliestMS > 0 {
		earliest = time.UnixMilli(status.EarliestMS).UTC().Format(time.RFC3339)
	}
	lastSuccess := ""
	if status.LastSuccessMS > 0 {
		lastSuccess = time.UnixMilli(status.LastSuccessMS).UTC().Format(time.RFC3339)
	}
	p.logger.Warn("audit push backlog",
		"pending", status.Pending,
		"quarantined", status.Quarantined,
		"earliest_pending", earliest,
		"last_success", lastSuccess,
	)
}

// sendOneBatch sends the oldest due events and reports how many rows left the queue.
// A zero count with a nil error means there was nothing due.
func (p *Pusher) sendOneBatch(ctx context.Context) (int, error) {
	now := p.now()
	events, err := p.repository.ListPendingAuditPushes(ctx, now.UnixMilli(), p.maxBatch)
	if err != nil {
		return 0, err
	}
	if len(events) == 0 {
		return 0, nil
	}
	ids := make([]int64, 0, len(events))
	payloads := make([][]byte, 0, len(events))
	for _, event := range events {
		ids = append(ids, event.ID)
		payloads = append(payloads, event.Payload)
	}

	response, statusCode, body, err := p.post(ctx, payloads)
	if err != nil {
		return 0, p.fail(ctx, events, now, fmt.Sprintf("send failed: %v", err))
	}
	switch {
	case statusCode == http.StatusBadRequest || statusCode == http.StatusRequestEntityTooLarge || statusCode == http.StatusUnsupportedMediaType:
		// The contract says these are format, field or size problems: retrying the
		// same bytes cannot succeed, so keep them out of the replay queue but visible.
		return 0, p.quarantine(ctx, ids, now, fmt.Sprintf("rejected with HTTP %d: %s", statusCode, excerpt(body)))
	case statusCode < 200 || statusCode > 299:
		return 0, p.fail(ctx, events, now, fmt.Sprintf("unexpected HTTP %d: %s", statusCode, excerpt(body)))
	}

	// 202 means the request was processed, not that every event was accepted.
	accepted := make([]int64, 0, len(ids))
	rejected := make([]int64, 0)
	rejectedReasons := make(map[int64]string)
	if response.Rejected <= 0 && len(response.Errors) == 0 {
		accepted = ids
	} else {
		failed := make(map[int]string, len(response.Errors))
		unmapped := false
		for _, raw := range response.Errors {
			index, message, ok := parseErrorIndex(raw)
			if !ok || index < 0 || index >= len(ids) {
				unmapped = true
				continue
			}
			failed[index] = message
		}
		if unmapped {
			// A rejection we cannot attribute keeps the whole batch queued; the
			// platform deduplicates, so an extra delivery is harmless and losing an
			// event is not.
			return 0, p.fail(ctx, events, now, "platform rejected events without usable indices")
		}
		for index, id := range ids {
			if message, ok := failed[index]; ok {
				rejected = append(rejected, id)
				rejectedReasons[id] = message
				continue
			}
			accepted = append(accepted, id)
		}
	}
	if err := p.repository.MarkAuditPushesSent(ctx, accepted, now.UnixMilli()); err != nil {
		return 0, err
	}
	// A per-event rejection is a payload problem; the same bytes will be rejected
	// again, so it is quarantined rather than replayed.
	if len(rejected) > 0 {
		if err := p.repository.QuarantineAuditPushes(ctx, rejected, now.UnixMilli(), joinReasons(rejectedReasons)); err != nil {
			return 0, err
		}
	}
	return len(accepted) + len(rejected), nil
}

func (p *Pusher) post(ctx context.Context, payloads [][]byte) (ingestResponse, int, []byte, error) {
	body := make([]byte, 0, 256*len(payloads))
	body = append(body, `{"events":[`...)
	for index, payload := range payloads {
		if index > 0 {
			body = append(body, ',')
		}
		body = append(body, payload...)
	}
	body = append(body, `]}`...)

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return ingestResponse{}, 0, nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := p.client.Do(request)
	if err != nil {
		return ingestResponse{}, 0, nil, err
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return ingestResponse{}, response.StatusCode, nil, err
	}
	var decoded ingestResponse
	if response.StatusCode >= 200 && response.StatusCode <= 299 {
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return ingestResponse{}, response.StatusCode, raw, fmt.Errorf("decode ingest response: %w", err)
		}
	}
	return decoded, response.StatusCode, raw, nil
}

// fail records a failed delivery. With retries disabled the events leave the replay
// queue so the status stays honest about what will never be sent by this process.
func (p *Pusher) fail(ctx context.Context, events []repository.QueuedAuditPush, now time.Time, reason string) error {
	ids := make([]int64, 0, len(events))
	attempts := 0
	for _, event := range events {
		ids = append(ids, event.ID)
		if event.Attempts > attempts {
			attempts = event.Attempts
		}
	}
	if !p.retry {
		if err := p.repository.QuarantineAuditPushes(ctx, ids, now.UnixMilli(), "retry disabled: "+reason); err != nil {
			return err
		}
		return errors.New(reason)
	}
	next := now.Add(backoff(attempts))
	if err := p.repository.RescheduleAuditPushes(ctx, ids, now.UnixMilli(), next.UnixMilli(), reason); err != nil {
		return err
	}
	return errors.New(reason)
}

func (p *Pusher) quarantine(ctx context.Context, ids []int64, now time.Time, reason string) error {
	if err := p.repository.QuarantineAuditPushes(ctx, ids, now.UnixMilli(), reason); err != nil {
		return err
	}
	return errors.New(reason)
}

func backoff(attempts int) time.Duration {
	delay := baseBackoff
	for i := 0; i < attempts && delay < maxBackoff; i++ {
		delay *= 2
	}
	if delay > maxBackoff {
		delay = maxBackoff
	}
	return delay
}

type ingestResponse struct {
	Accepted int               `json:"accepted"`
	Rejected int               `json:"rejected"`
	Errors   []json.RawMessage `json:"errors"`
}

func parseErrorIndex(raw json.RawMessage) (int, string, bool) {
	var detail struct {
		Index   *int   `json:"index"`
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(raw, &detail); err != nil || detail.Index == nil {
		return 0, "", false
	}
	message := detail.Message
	if message == "" {
		message = detail.Error
	}
	return *detail.Index, strings.TrimSpace(message), true
}

func joinReasons(reasons map[int64]string) string {
	if len(reasons) == 0 {
		return "rejected by the platform"
	}
	parts := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		if reason == "" {
			continue
		}
		parts = append(parts, reason)
	}
	if len(parts) == 0 {
		return "rejected by the platform"
	}
	return strings.Join(parts, "; ")
}

func excerpt(body []byte) string {
	text := strings.TrimSpace(string(body))
	if len(text) > 200 {
		text = text[:200]
	}
	return text
}

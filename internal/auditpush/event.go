// Package auditpush forwards the console audit trail to the team audit-logger service.
//
// The platform contract is LambdaTheory/audit-logger-agent's
// docs/agent-audit-log-integration-guide.md: OMC is the upstream producer, the
// platform never pulls, and every event must carry the canonical fields below. The
// guide's naming is kept verbatim here so a reviewer can compare the two without a
// translation step.
//
// The flow is the guide's: the local audit record is written first, the platform copy
// is queued in the same transaction, and a background pusher replays the stored
// payload unchanged. A platform outage therefore never affects an approval, a
// delivery or a stop - the console's own records stay the source of truth (PRD 4.8).
package auditpush

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// AgentID is OMC's stable producer identity on the platform. The guide allows only
	// letters, digits, '.', '_' and '-'.
	AgentID = "oh-my-cpa"
	// EventToolEnd is the canonical event name for a completed operation. Each OMC
	// action is one completed step, so it is recorded as tool.end rather than a
	// start/end pair the platform would later report as a broken chain.
	EventToolEnd = "tool.end"
	// EventToolError is used when the step itself failed.
	EventToolError = "tool.error"
	// SummaryLimit is the platform's cap on result_summary, in characters.
	SummaryLimit = 200
	// MaxEventBytes is the platform's default per-event ceiling (maxLineBytes, 64 KiB).
	MaxEventBytes = 64 << 10
	// execution environment channel values OMC reports.
	ChannelMCP     = "mcp"
	ChannelConsole = "console"
)

// Statuses are the canonical gRPC codes the platform accepts. Anything else is stored
// as "unknown" by the platform, which would silently weaken its review rules.
var Statuses = map[string]bool{
	"OK": true, "CANCELLED": true, "UNKNOWN": true, "INVALID_ARGUMENT": true,
	"DEADLINE_EXCEEDED": true, "NOT_FOUND": true, "ALREADY_EXISTS": true,
	"PERMISSION_DENIED": true, "RESOURCE_EXHAUSTED": true, "FAILED_PRECONDITION": true,
	"ABORTED": true, "OUT_OF_RANGE": true, "UNIMPLEMENTED": true, "INTERNAL": true,
	"UNAVAILABLE": true, "DATA_LOSS": true, "UNAUTHENTICATED": true,
}

// Entity identifies the business object an event is about. The guide requires a
// repository to be reported through this field rather than a bespoke `repo` key.
type Entity struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// ErrorDetail carries a redacted failure summary. The guide forbids a top-level
// error code: the canonical `status` already says what kind of failure it was.
type ErrorDetail struct {
	Message string `json:"message"`
}

// Event is one platform audit event. The field order and names match the guide's
// minimum event; optional fields are omitted rather than sent empty.
type Event struct {
	TS            string       `json:"ts"`
	AgentID       string       `json:"agent_id"`
	TraceID       string       `json:"trace_id"`
	SpanID        string       `json:"span_id"`
	Event         string       `json:"event"`
	ToolName      string       `json:"tool_name"`
	Status        string       `json:"status"`
	ResultSummary string       `json:"result_summary"`
	Entity        *Entity      `json:"entity,omitempty"`
	Tags          []string     `json:"tags,omitempty"`
	Channel       string       `json:"channel,omitempty"`
	Error         *ErrorDetail `json:"error,omitempty"`
}

// Description is what a caller knows about one finished step. Build turns it into an
// event with a fresh timestamp and span id; neither is ever regenerated on retry,
// because the stored payload is replayed verbatim.
type Description struct {
	// Action is the stable tool name, e.g. "repo_key.apply". It must be the same
	// string every time that step happens, which is what makes the platform's tool
	// statistics and allowlists meaningful.
	Action string
	// TraceID links the steps of one flow. For a key application that is the
	// application id from submission through delivery; for key-level actions it is
	// the binding id.
	TraceID string
	// Summary is a redacted, human-readable result. It is truncated to the
	// platform's limit rather than rejected, and must never hold key material.
	Summary string
	// Status is a canonical gRPC code; empty means OK.
	Status string
	// RepoID is the repository's remote identity (or local name when it has no
	// remote), reported as entity {type: "repo"}.
	RepoID string
	// Tags carry the caller's agent and device so the platform can filter by them
	// without parsing the summary.
	Tags []string
	// Channel is "mcp" or "console".
	Channel string
	// ErrorMessage is set only for a failed step.
	ErrorMessage string
}

// Build validates a description and returns the event plus its serialized payload.
// The payload is the exact bytes the platform receives and the exact bytes stored for
// retry, so a replay cannot differ from the original in field order or values.
func Build(description Description) (Event, []byte, error) {
	action := strings.TrimSpace(description.Action)
	if action == "" {
		return Event{}, nil, errors.New("audit push action is required")
	}
	traceID := strings.TrimSpace(description.TraceID)
	if traceID == "" {
		return Event{}, nil, errors.New("audit push trace id is required")
	}
	status := strings.TrimSpace(description.Status)
	if status == "" {
		status = "OK"
	}
	if !Statuses[status] {
		return Event{}, nil, fmt.Errorf("audit push status %q is not a canonical gRPC code", status)
	}
	eventName := EventToolEnd
	if status != "OK" {
		eventName = EventToolError
	}
	spanID, err := newSpanID()
	if err != nil {
		return Event{}, nil, err
	}
	event := Event{
		TS:            time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		AgentID:       AgentID,
		TraceID:       traceID,
		SpanID:        spanID,
		Event:         eventName,
		ToolName:      action,
		Status:        status,
		ResultSummary: truncate(description.Summary, SummaryLimit),
		Tags:          description.Tags,
		Channel:       strings.TrimSpace(description.Channel),
	}
	if repoID := strings.TrimSpace(description.RepoID); repoID != "" {
		event.Entity = &Entity{Type: "repo", ID: repoID}
	}
	if message := strings.TrimSpace(description.ErrorMessage); message != "" {
		event.Error = &ErrorDetail{Message: truncate(message, SummaryLimit)}
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return Event{}, nil, fmt.Errorf("encode audit push event: %w", err)
	}
	if len(payload) > MaxEventBytes {
		return Event{}, nil, fmt.Errorf("audit push event is %d bytes, over the platform limit of %d", len(payload), MaxEventBytes)
	}
	return event, payload, nil
}

func newSpanID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate audit span id: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

func truncate(value string, limit int) string {
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit])
}

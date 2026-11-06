package auditpush

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestBuildProducesThePlatformContract checks the required fields from the integration
// guide, in one place, so a later edit cannot quietly drop one.
func TestBuildProducesThePlatformContract(t *testing.T) {
	event, payload, err := Build(Description{
		Action: "repo_key.stop", TraceID: "binding-1", RepoID: "org/repo",
		Summary: "stopped key sk-…abcd for org/repo (leaked): lost laptop",
		Tags:    repoKeyTagValues("claude", "homan-looplay"),
		Channel: ChannelConsole,
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	for field, value := range map[string]string{
		"agent_id": AgentID, "trace_id": "binding-1", "event": EventToolEnd,
		"tool_name": "repo_key.stop", "status": "OK", "channel": ChannelConsole,
	} {
		var decoded map[string]any
		if err := json.Unmarshal(payload, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded[field] != value {
			t.Fatalf("%s = %v, want %s", field, decoded[field], value)
		}
	}
	if event.Entity == nil || event.Entity.Type != "repo" || event.Entity.ID != "org/repo" {
		t.Fatalf("entity = %+v", event.Entity)
	}
	if len(event.Tags) != 2 || event.Tags[0] != "agent:claude" {
		t.Fatalf("tags = %v", event.Tags)
	}
	if !strings.Contains(event.TS, "T") || !strings.HasSuffix(event.TS, "Z") {
		t.Fatalf("ts = %q, want an ISO 8601 UTC timestamp", event.TS)
	}
	if len(event.SpanID) != 32 {
		t.Fatalf("span id = %q, want 32 hex characters", event.SpanID)
	}
}

// TestBuildUsesTheErrorEventForFailures keeps the event name and status consistent: a
// failed step is tool.error with a canonical failure status, never tool.end with an
// error message attached.
func TestBuildUsesTheErrorEventForFailures(t *testing.T) {
	event, _, err := Build(Description{
		Action: "repo_key.reveal", TraceID: "binding-1", Status: "PERMISSION_DENIED",
		// The platform forbids an error code; the status carries it and message explains it.
		ErrorMessage: "operator is not signed in",
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if event.Event != EventToolError {
		t.Fatalf("event = %q, want %q", event.Event, EventToolError)
	}
	if event.Error == nil || event.Error.Message != "operator is not signed in" {
		t.Fatalf("error = %+v", event.Error)
	}
}

// TestBuildRefusesNonCanonicalInput catches the mistakes that would otherwise be stored
// by the platform as "unknown" and silently weaken its review.
func TestBuildRefusesNonCanonicalInput(t *testing.T) {
	cases := []Description{
		{Action: "", TraceID: "t"},
		{Action: "repo_key.apply", TraceID: ""},
		{Action: "repo_key.apply", TraceID: "t", Status: "success"},
		{Action: "repo_key.apply", TraceID: "t", Status: "ok"},
	}
	for index, description := range cases {
		if _, _, err := Build(description); err == nil {
			t.Fatalf("case %d (%+v) should have been refused", index, description)
		}
	}
}

// TestBuildTruncatesTheSummaryInsteadOfRejecting is the guide's requirement that a
// summary is a redacted digest; an over-long one is trimmed, not dropped.
func TestBuildTruncatesTheSummaryInsteadOfRejecting(t *testing.T) {
	event, _, err := Build(Description{
		Action: "repo_key.apply", TraceID: "t", Summary: strings.Repeat("summary ", 100),
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len([]rune(event.ResultSummary)) != SummaryLimit {
		t.Fatalf("summary length = %d, want %d", len([]rune(event.ResultSummary)), SummaryLimit)
	}
}

func repoKeyTagValues(agent, device string) []string {
	return []string{"agent:" + agent, "device:" + device}
}

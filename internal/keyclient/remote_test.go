package keyclient

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeCapability answers the capability invoke endpoint the way OMC does: the tool name
// and arguments in the body, a Result-shaped answer out. It is the smallest server that
// exercises the bridge's request, authorization and unwrapping without a real OMC.
type fakeCapability struct {
	server *httptest.Server
	// answers maps a tool name to the data it returns; a name mapped to nil is refused
	// with the code in refusals.
	answers  map[string]any
	refusals map[string]string
	// status is the HTTP status the next request gets; zero means 200.
	status int
	// calls records what the client asked for, so a test can assert the arguments.
	calls []map[string]any
}

func newFakeCapability(t *testing.T) *fakeCapability {
	t.Helper()
	fake := &fakeCapability{answers: map[string]any{}, refusals: map[string]string{}}
	fake.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer test-management-key" {
			writer.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(writer, `{"code":"unauthorized"}`)
			return
		}
		if fake.status != 0 {
			writer.WriteHeader(fake.status)
			_, _ = io.WriteString(writer, `{"code":"server_error"}`)
			return
		}
		payload, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
			return
		}
		var call struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(payload, &call); err != nil {
			t.Errorf("invoke body is not JSON: %v", err)
			return
		}
		var arguments map[string]any
		_ = json.Unmarshal(call.Arguments, &arguments)
		arguments["tool"] = call.Name
		fake.calls = append(fake.calls, arguments)
		if code, refused := fake.refusals[call.Name]; refused {
			writeResult(t, writer, map[string]any{"status": "error", "code": code})
			return
		}
		answer, ok := fake.answers[call.Name]
		if !ok {
			writeResult(t, writer, map[string]any{"status": "error", "code": "capability_forbidden"})
			return
		}
		writeResult(t, writer, map[string]any{"status": "success", "data": answer})
	}))
	t.Cleanup(fake.server.Close)
	return fake
}

func writeResult(t *testing.T, writer http.ResponseWriter, value map[string]any) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		t.Errorf("write result: %v", err)
	}
}

// statusAnswer is what repo_key_status returns for an approved application.
func statusAnswer(applicationID, status, kind, mask string, decidedAt time.Time) map[string]any {
	answer := map[string]any{
		"application_id": applicationID, "status": status, "kind": kind,
		"key_mask": mask, "repo": "org/repo", "repo_display": "Org/Repo",
	}
	if !decidedAt.IsZero() {
		answer["decided_at_ms"] = decidedAt.UnixMilli()
	}
	return answer
}

// queryAnswer is what repo_key_query returns for a repository holding an active key.
func queryAnswer(repo, state, mask string) map[string]any {
	return map[string]any{"repo": repo, "state": state, "key_mask": mask}
}

// refreshFixture records one repository with an application so `--remote` has something to
// refresh.
func refreshFixture(t *testing.T) (Home, *Store) {
	t.Helper()
	home := newTestHome(t)
	if _, err := Generate(GenOptions{Home: home, Repo: "org/repo", Now: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := AssociateApplication(home, "org/repo", "app-1", time.Now()); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStoreForUpdate(home, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return home, store
}

func bridgeClient(t *testing.T, fake *fakeCapability) RemoteClient {
	t.Helper()
	client, err := newBridgeRemote(fake.server.URL, "test-management-key", "", "")
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestRefreshRemoteUpdatesApprovalAndRepoState(t *testing.T) {
	_, store := refreshFixture(t)
	fake := newFakeCapability(t)
	decided := time.Date(2026, 11, 17, 9, 20, 0, 0, time.UTC)
	fake.answers[TOOL_STATUS] = statusAnswer("app-1", "approved", "issue", "sk-cpa-ab***wxyz", decided)
	fake.answers[TOOL_QUERY] = queryAnswer("org/repo", "active", "sk-cpa-ab***wxyz")
	at := time.Date(2026, 11, 17, 9, 30, 0, 0, time.UTC)
	failures, err := RefreshRemote(context.Background(), store, bridgeClient(t, fake), at)
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 0 {
		t.Fatalf("failures = %v", failures)
	}
	record, ok := store.Repo("org/repo")
	if !ok {
		t.Fatal("the repository vanished")
	}
	if record.ApprovalStatus != APPROVAL_APPROVED || record.StatusSource != STATUS_SOURCE_REMOTE {
		t.Fatalf("approval = %q/%q", record.ApprovalStatus, record.StatusSource)
	}
	if !record.DecidedAt.Equal(decided) || !record.StatusUpdatedAt.Equal(at) {
		t.Fatalf("decided/updated = %v/%v", record.DecidedAt, record.StatusUpdatedAt)
	}
	if record.RepoState != REPO_STATE_ACTIVE || !record.RepoStateUpdatedAt.Equal(at) {
		t.Fatalf("repo state = %q at %v", record.RepoState, record.RepoStateUpdatedAt)
	}
	// The application id must have travelled as the argument, not been guessed.
	if len(fake.calls) != 2 {
		t.Fatalf("calls = %v", fake.calls)
	}
	if fake.calls[0]["application_id"] != "app-1" || fake.calls[0]["tool"] != TOOL_STATUS {
		t.Fatalf("status call = %v", fake.calls[0])
	}
	if fake.calls[1]["repo"] != "org/repo" || fake.calls[1]["tool"] != TOOL_QUERY {
		t.Fatalf("query call = %v", fake.calls[1])
	}
}

func TestRefreshRemoteKeepsLocalStateWhenOneReadFails(t *testing.T) {
	_, store := refreshFixture(t)
	fake := newFakeCapability(t)
	fake.answers[TOOL_QUERY] = queryAnswer("org/repo", "stopped", "")
	fake.refusals[TOOL_STATUS] = "repo_key_application_not_found"
	failures, err := RefreshRemote(context.Background(), store, bridgeClient(t, fake), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 1 || failures[0].Repo != "org/repo" || failures[0].Code != CODE_REMOTE_UNAVAILABLE {
		t.Fatalf("failures = %+v", failures)
	}
	summary := RefreshSummary(failures)
	if !strings.Contains(summary, "remote_error:"+CODE_REMOTE_UNAVAILABLE) {
		t.Fatalf("summary = %q", summary)
	}
	record, _ := store.Repo("org/repo")
	// The rejected read leaves the approval as it was (unknown, from the association), and
	// the successful read still updates the repository state.
	if record.ApprovalStatus != APPROVAL_UNKNOWN || record.StatusSource != STATUS_SOURCE_LOCAL {
		t.Fatalf("approval = %q/%q", record.ApprovalStatus, record.StatusSource)
	}
	if record.RepoState != REPO_STATE_STOPPED {
		t.Fatalf("repo state = %q", record.RepoState)
	}
	// The report must not present the stale approval as a current remote value.
	home := store.Home()
	report, err := BuildReport(home, BaseURL{Source: "absent"}, "")
	if err != nil {
		t.Fatal(err)
	}
	report.ApplyRemoteFailures(failures)
	row := reportFor(t, report, "org/repo")
	if row.RemoteError != "remote_error:"+CODE_REMOTE_UNAVAILABLE {
		t.Fatalf("remote error = %q", row.RemoteError)
	}
	var output bytes.Buffer
	WriteReport(&output, report)
	if !strings.Contains(output.String(), "the values above are the last local state") {
		t.Fatalf("the report does not flag stale values:\n%s", output.String())
	}
}

func TestRefreshRemoteReportsATransportFailure(t *testing.T) {
	_, store := refreshFixture(t)
	fake := newFakeCapability(t)
	fake.status = http.StatusInternalServerError
	failures, err := RefreshRemote(context.Background(), store, bridgeClient(t, fake), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 2 {
		t.Fatalf("both reads against a 500 should fail, got %+v", failures)
	}
	for _, failure := range failures {
		if failure.Code != CODE_REMOTE_UNAVAILABLE {
			t.Fatalf("code = %q", failure.Code)
		}
	}
}

func TestRefreshRemoteDoesNotRepeatTheResponseBody(t *testing.T) {
	_, store := refreshFixture(t)
	fake := newFakeCapability(t)
	fake.refusals[TOOL_STATUS] = "repo_key_application_not_found"
	fake.answers[TOOL_QUERY] = queryAnswer("org/repo", "active", "")
	failures, err := RefreshRemote(context.Background(), store, bridgeClient(t, fake), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, failure := range failures {
		if strings.Contains(failure.Error(), "<") || strings.Contains(failure.Error(), "http") {
			t.Fatalf("a failure repeated transport detail: %q", failure.Error())
		}
	}
}

// clearRemoteEnvironment makes the configuration test independent of the machine it runs
// on: a developer with OMCPA_SERVER_URL set must still see the missing-variable refusal.
func clearRemoteEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{ENV_SERVER_URL, ENV_MANAGEMENT_KEY, ENV_AGENT_NAME, ENV_DEVICE_NAME} {
		t.Setenv(name, "")
	}
}

func TestNewRemoteClientRequiresBothVariables(t *testing.T) {
	clearRemoteEnvironment(t)
	if _, err := NewRemoteClient(); CodeOf(err) != CODE_REMOTE_NOT_CONFIGURED {
		t.Fatalf("code = %q (err=%v)", CodeOf(err), err)
	}
	t.Setenv(ENV_SERVER_URL, "https://omc.example.test")
	if _, err := NewRemoteClient(); CodeOf(err) != CODE_REMOTE_NOT_CONFIGURED {
		t.Fatalf("with only the URL: code = %q (err=%v)", CodeOf(err), err)
	}
	if message := MessageOf(mustRemoteError(t)); !strings.Contains(message, ENV_MANAGEMENT_KEY) {
		t.Fatalf("the refusal should name the missing variable: %q", message)
	}
	t.Setenv(ENV_SERVER_URL, "")
	t.Setenv(ENV_MANAGEMENT_KEY, "test-management-key")
	if _, err := NewRemoteClient(); CodeOf(err) != CODE_REMOTE_NOT_CONFIGURED {
		t.Fatalf("with only the key: code = %q (err=%v)", CodeOf(err), err)
	}
}

func mustRemoteError(t *testing.T) error {
	t.Helper()
	_, err := NewRemoteClient()
	if err == nil {
		t.Fatal("NewRemoteClient should fail without both variables")
	}
	return err
}

func TestNewRemoteClientRefusesPlainRemoteHTTP(t *testing.T) {
	clearRemoteEnvironment(t)
	t.Setenv(ENV_SERVER_URL, "http://omc.example.test")
	t.Setenv(ENV_MANAGEMENT_KEY, "test-management-key")
	if _, err := NewRemoteClient(); CodeOf(err) != CODE_REMOTE_NOT_CONFIGURED {
		t.Fatalf("code = %q (err=%v)", CodeOf(err), err)
	}
}

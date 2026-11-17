package keyclient

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/capability"
	"github.com/oh-my-cpa/oh-my-cpa/internal/mcpbridge"
)

const (
	// The console URL and management key `key status --remote` uses. They are the same
	// pair `oh-my-cpa mcp` reads, so a machine already set up for the MCP bridge needs no
	// second configuration.
	ENV_SERVER_URL     = "OMCPA_SERVER_URL"
	ENV_MANAGEMENT_KEY = "OMCPA_CPA_MANAGEMENT_KEY"
	ENV_AGENT_NAME     = "OMCPA_AGENT_NAME"
	ENV_DEVICE_NAME    = "OMCPA_DEVICE_NAME"

	// TOOL_STATUS and TOOL_QUERY are the two read-only MCP capabilities this client calls.
	TOOL_STATUS = "repo_key_status"
	TOOL_QUERY  = "repo_key_query"
)

// RemoteApplication is one application's server-side state, reduced to what the local
// report shows.
type RemoteApplication struct {
	ApplicationID   string
	Status          string
	Kind            string
	KeyMask         string
	Repo            string
	RepoDisplay     string
	DecidedAt       time.Time
	CiphertextReady bool
}

// RemoteRepoState is the gateway's view of the repository's key, which is separate from
// any one application's decision.
type RemoteRepoState struct {
	Repo    string
	State   string
	KeyMask string
}

// RepoRefreshError is one repository whose remote read failed. The error code is echoed
// because it is what a caller acts on; the response body is not.
type RepoRefreshError struct {
	Repo string
	Code string
}

func (e RepoRefreshError) Error() string {
	return e.Repo + ": " + e.Code
}

// RemoteClient is the read-only pair of MCP calls `key status --remote` makes. It is an
// interface so the refresh logic can be tested against a fake server without a network.
type RemoteClient interface {
	RepoKeyStatus(ctx context.Context, applicationID string) (RemoteApplication, error)
	RepoKeyQuery(ctx context.Context, repo string) (RemoteRepoState, error)
}

// remoteResult is the capability envelope both tools answer with.
type remoteResult struct {
	Status string          `json:"status"`
	Data   json.RawMessage `json:"data"`
	Code   string          `json:"code"`
	Detail string          `json:"detail"`
}

// remoteStatusData mirrors operations.RepoKeyStatusResult's wire shape.
type remoteStatusData struct {
	ApplicationID string `json:"application_id"`
	Status        string `json:"status"`
	Kind          string `json:"kind"`
	KeyMask       string `json:"key_mask"`
	Repo          string `json:"repo"`
	RepoDisplay   string `json:"repo_display"`
	DecidedAtMS   int64  `json:"decided_at_ms"`
}

// remoteQueryData mirrors operations.RepoKeyQueryResult's wire shape.
type remoteQueryData struct {
	Repo    string `json:"repo"`
	State   string `json:"state"`
	KeyMask string `json:"key_mask"`
}

// bridgeRemote is the RemoteClient backed by the MCP bridge: the same authenticated
// capability transport, HTTPS rule and redirect refusal `oh-my-cpa mcp` uses.
type bridgeRemote struct {
	bridge *mcpbridge.Bridge
}

// NewRemoteClient builds a client from the environment.
//
// A missing URL or key is remote_not_configured, never a silent fallback to the local
// view: a person who asked for the remote state must be told the request did not happen.
func NewRemoteClient() (RemoteClient, error) {
	return newBridgeRemote(
		strings.TrimSpace(os.Getenv(ENV_SERVER_URL)),
		strings.TrimSpace(os.Getenv(ENV_MANAGEMENT_KEY)),
		strings.TrimSpace(os.Getenv(ENV_AGENT_NAME)),
		strings.TrimSpace(os.Getenv(ENV_DEVICE_NAME)),
	)
}

// newBridgeRemote builds the client from explicit values, which is how a test points it
// at its own server without changing the process environment.
func newBridgeRemote(serverURL, managementKey, agentName, deviceName string) (RemoteClient, error) {
	if serverURL == "" || managementKey == "" {
		missing := make([]string, 0, 2)
		if serverURL == "" {
			missing = append(missing, ENV_SERVER_URL)
		}
		if managementKey == "" {
			missing = append(missing, ENV_MANAGEMENT_KEY)
		}
		return nil, Errorf(CODE_REMOTE_NOT_CONFIGURED, "--remote needs %s; nothing was changed", strings.Join(missing, " and "))
	}
	bridge, err := mcpbridge.New(serverURL, managementKey)
	if err != nil {
		return nil, Wrapf(err, CODE_REMOTE_NOT_CONFIGURED, "%s or %s is not usable: %s", ENV_SERVER_URL, ENV_MANAGEMENT_KEY, err)
	}
	bridge.WithCaller(capability.Caller{Agent: agentName, Device: deviceName})
	return &bridgeRemote{bridge: bridge}, nil
}

// invoke runs one read capability and unwraps its data field.
func (client *bridgeRemote) invoke(ctx context.Context, tool string, arguments any) (json.RawMessage, error) {
	encoded, err := json.Marshal(arguments)
	if err != nil {
		return nil, Errorf(CODE_REMOTE_UNAVAILABLE, "cannot encode the %s request", tool)
	}
	result, err := client.bridge.Invoke(ctx, tool, encoded)
	if err != nil {
		return nil, remoteError(err)
	}
	switch result.Status {
	case "success":
		return result.Data, nil
	case "error":
		code := result.Code
		if !refusalCodePattern.MatchString(code) {
			code = "refused"
		}
		return nil, Errorf(CODE_REMOTE_UNAVAILABLE, "OMC refused %s: %s", tool, code)
	default:
		return nil, Errorf(CODE_REMOTE_UNAVAILABLE, "OMC answered %s with status %q", tool, result.Status)
	}
}

// remoteError turns a bridge failure into the report's two shapes: a stable refusal code
// that is safe to repeat, or a transport failure that is not.
func remoteError(err error) error {
	message := err.Error()
	if refusalCodePattern.MatchString(message) && message != "omc_unavailable" {
		return Errorf(CODE_REMOTE_UNAVAILABLE, "OMC refused the read: %s", message)
	}
	return Errorf(CODE_REMOTE_UNAVAILABLE, "cannot reach OMC: %s", message)
}

// RepoKeyStatus reads one application.
func (client *bridgeRemote) RepoKeyStatus(ctx context.Context, applicationID string) (RemoteApplication, error) {
	data, err := client.invoke(ctx, TOOL_STATUS, map[string]string{"application_id": applicationID})
	if err != nil {
		return RemoteApplication{}, err
	}
	var decoded remoteStatusData
	if err := json.Unmarshal(data, &decoded); err != nil {
		return RemoteApplication{}, Errorf(CODE_REMOTE_UNAVAILABLE, "OMC answered %s with an unreadable body", TOOL_STATUS)
	}
	application := RemoteApplication{
		ApplicationID: decoded.ApplicationID, Status: decoded.Status, Kind: decoded.Kind,
		KeyMask: decoded.KeyMask, Repo: decoded.Repo, RepoDisplay: decoded.RepoDisplay,
	}
	if decoded.DecidedAtMS > 0 {
		application.DecidedAt = time.UnixMilli(decoded.DecidedAtMS).UTC()
	}
	return application, nil
}

// RepoKeyQuery reads the repository's key state.
func (client *bridgeRemote) RepoKeyQuery(ctx context.Context, repo string) (RemoteRepoState, error) {
	data, err := client.invoke(ctx, TOOL_QUERY, map[string]string{"repo": repo})
	if err != nil {
		return RemoteRepoState{}, err
	}
	var decoded remoteQueryData
	if err := json.Unmarshal(data, &decoded); err != nil {
		return RemoteRepoState{}, Errorf(CODE_REMOTE_UNAVAILABLE, "OMC answered %s with an unreadable body", TOOL_QUERY)
	}
	return RemoteRepoState{Repo: decoded.Repo, State: decoded.State, KeyMask: decoded.KeyMask}, nil
}

// remoteApproval maps an application status onto the local vocabulary. An unknown value
// becomes "unknown" rather than being stored verbatim: the field is a closed set a later
// command reads, and a new server status must not arrive as an invented local state.
func remoteApproval(status string) string {
	switch strings.TrimSpace(status) {
	case "pending":
		return APPROVAL_PENDING
	case "approved":
		return APPROVAL_APPROVED
	case "rejected":
		return APPROVAL_REJECTED
	case "stopped":
		return APPROVAL_STOPPED
	default:
		return APPROVAL_UNKNOWN
	}
}

// remoteRepoState maps the query's three-way answer onto the local vocabulary.
func remoteRepoState(state string) string {
	switch strings.TrimSpace(state) {
	case REPO_STATE_ACTIVE:
		return REPO_STATE_ACTIVE
	case REPO_STATE_STOPPED:
		return REPO_STATE_STOPPED
	case REPO_STATE_NONE, "":
		return REPO_STATE_NONE
	default:
		return REPO_STATE_UNKNOWN
	}
}

// RefreshRemote updates every recorded repository from OMC and saves the store.
//
// Each repository is refreshed on its own: one failed read is reported and the others
// still update, because a person asking "what does OMC think" benefits more from a
// partial answer with the failure named than from no answer at all.
func RefreshRemote(ctx context.Context, store *Store, client RemoteClient, now time.Time) ([]RepoRefreshError, error) {
	now = now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	repoIDs := make([]string, 0, len(store.Repos()))
	for repoID := range store.Repos() {
		repoIDs = append(repoIDs, repoID)
	}
	sort.Strings(repoIDs)
	failures := make([]RepoRefreshError, 0)
	changed := false
	for _, repoID := range repoIDs {
		record, ok := store.Repo(repoID)
		if !ok {
			continue
		}
		if applicationID := strings.TrimSpace(record.ApplicationID); applicationID != "" {
			application, err := client.RepoKeyStatus(ctx, applicationID)
			if err != nil {
				failures = append(failures, RepoRefreshError{Repo: repoID, Code: codeOfRemote(err)})
			} else {
				record.ApprovalStatus = remoteApproval(application.Status)
				record.StatusSource = STATUS_SOURCE_REMOTE
				record.StatusUpdatedAt = now
				if application.Kind != "" {
					record.ApplicationKind = application.Kind
				}
				if application.KeyMask != "" {
					record.KeyMask = application.KeyMask
				}
				if !application.DecidedAt.IsZero() {
					record.DecidedAt = application.DecidedAt
				}
				changed = true
			}
		}
		repoState, err := client.RepoKeyQuery(ctx, repoID)
		if err != nil {
			failures = append(failures, RepoRefreshError{Repo: repoID, Code: codeOfRemote(err)})
		} else {
			record.RepoState = remoteRepoState(repoState.State)
			record.RepoStateUpdatedAt = now
			if record.KeyMask == "" && repoState.KeyMask != "" {
				record.KeyMask = repoState.KeyMask
			}
			changed = true
		}
		if err := store.PutRepo(record); err != nil {
			return failures, err
		}
	}
	if changed {
		if err := store.Save(); err != nil {
			return failures, err
		}
	}
	return failures, nil
}

// codeOfRemote extracts the stable code a refresh failure is reported under.
func codeOfRemote(err error) string {
	code := CodeOf(err)
	if code == "" {
		return CODE_REMOTE_UNAVAILABLE
	}
	return code
}

// RefreshSummary renders the failure list as the one line a command prints after the
// report. Each entry keeps its own marker (`remote_error:<code>`) so a reader can tell
// which repository still shows local state and why, without the rest of the list losing
// its remote values.
func RefreshSummary(failures []RepoRefreshError) string {
	if len(failures) == 0 {
		return ""
	}
	parts := make([]string, 0, len(failures))
	for _, failure := range failures {
		parts = append(parts, failure.Repo+" (remote_error:"+failure.Code+")")
	}
	return "some repositories could not be refreshed and still show their last local state: " + strings.Join(parts, ", ")
}

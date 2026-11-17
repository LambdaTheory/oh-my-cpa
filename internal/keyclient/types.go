// Package keyclient is the caller side of the repository-owned key flow: where a
// private key and its delivery envelope live on this machine, the local state that
// remembers which repositories they belong to, and the operations that generate,
// store and later remove them.
//
// Nothing here prints or persists plaintext. A private key is 32 raw bytes on disk,
// an envelope is ciphertext, and the gateway key exists only in the child process a
// later `key exec` starts. The package deals in file paths, not file contents, for
// everything that is secret.
package keyclient

import "time"

const (
	// ENV_CPA_BASE_URL is the deployment-wide gateway address. The key flow reuses it
	// rather than reading the service configuration, because `key` runs before any of
	// that is loaded.
	ENV_CPA_BASE_URL = "OMCPA_CPA_BASE_URL"
)

// Runtime names a tool that can consume a repository key. The three values are the
// wire values of `--runtime` and the keys of State.Runtimes.
type Runtime string

const (
	RuntimeClaude   Runtime = "claude"
	RuntimeCodex    Runtime = "codex"
	RuntimeOpenCode Runtime = "opencode"
)

// Runtimes returns the supported runtimes in a stable order, so output and
// iteration do not depend on map order.
func Runtimes() []Runtime {
	return []Runtime{RuntimeClaude, RuntimeCodex, RuntimeOpenCode}
}

// ParseRuntime accepts one of the three runtime values. An unknown value is a usage
// error (runtime_unknown), not a fallback: guessing would inject the wrong variables.
func ParseRuntime(value string) (Runtime, error) {
	for _, runtime := range Runtimes() {
		if string(runtime) == value {
			return runtime, nil
		}
	}
	return "", Errorf(CODE_RUNTIME_UNKNOWN, "unknown runtime %q; use claude, codex or opencode", value)
}

// Values of RepoState.ApprovalStatus. They describe the last thing this machine
// learned about the repository's application, not the gateway's live view.
const (
	APPROVAL_NOT_APPLIED = "not_applied"
	APPROVAL_UNKNOWN     = "unknown"
	APPROVAL_PENDING     = "pending"
	APPROVAL_APPROVED    = "approved"
	APPROVAL_REJECTED    = "rejected"
	APPROVAL_STOPPED     = "stopped"
)

// Values of RepoState.StatusSource: where the last approval status came from.
const (
	STATUS_SOURCE_GEN    = "gen"
	STATUS_SOURCE_LOCAL  = "local"
	STATUS_SOURCE_REMOTE = "remote"
	STATUS_SOURCE_ACCEPT = "accept"
)

// Values of RepoState.RepoState: the gateway's view of the repository's key, which
// is distinct from one application's approval status.
const (
	REPO_STATE_ACTIVE  = "active"
	REPO_STATE_STOPPED = "stopped"
	REPO_STATE_NONE    = "none"
	REPO_STATE_UNKNOWN = "unknown"
)

// Values of RepoState.ApplicationKind.
const (
	APPLICATION_KIND_ISSUE   = "issue"
	APPLICATION_KIND_REUSE   = "reuse"
	APPLICATION_KIND_REISSUE = "reissue"
)

// RepoState is one repository's local record: the key it owns, the file that holds
// it, and the last application state this machine saw.
//
// Time fields are marshalled as RFC 3339 UTC and omitted when unset. The record never
// holds a private key, a plaintext gateway key, an envelope body or an approval token.
type RepoState struct {
	Repo        string `json:"repo"`
	RepoDisplay string `json:"repo_display"`

	// PrivateKeyFile and EnvelopeFile are paths relative to $OMC_HOME ("keys/...",
	// "envelopes/..."). They are stored rather than recomputed so a future filename
	// rule can still find an existing key.
	PrivateKeyFile string    `json:"private_key_file,omitempty"`
	PublicKey      string    `json:"public_key,omitempty"`
	Fingerprint    string    `json:"public_key_fingerprint,omitempty"`
	CreatedAt      time.Time `json:"created_at,omitzero"`

	ApplicationID   string    `json:"application_id,omitempty"`
	ApplicationKind string    `json:"application_kind,omitempty"`
	ApprovalStatus  string    `json:"approval_status,omitempty"`
	StatusSource    string    `json:"status_source,omitempty"`
	StatusUpdatedAt time.Time `json:"status_updated_at,omitzero"`
	KeyMask         string    `json:"key_mask,omitempty"`

	// DecidedAt is when the operator decided the recorded application, as OMC reported
	// it. It is the decision's own time, not when this machine heard about it, which is
	// StatusUpdatedAt.
	DecidedAt time.Time `json:"decided_at,omitzero"`

	// ValueFingerprint identifies the plaintext gateway key without holding it: it is
	// what lets a later purge decide whether a configured value or a backup is this
	// repository's key after the private key is gone.
	ValueFingerprint string `json:"value_fingerprint,omitempty"`

	RepoState          string    `json:"repo_state,omitempty"`
	RepoStateUpdatedAt time.Time `json:"repo_state_updated_at,omitzero"`

	EnvelopeFile       string    `json:"envelope_file,omitempty"`
	EnvelopeReceivedAt time.Time `json:"envelope_received_at,omitzero"`
	EnvelopeAlgorithm  string    `json:"envelope_algorithm,omitempty"`
}

// RuntimeSlot is the single place one runtime currently holds this machine's key.
// Each runtime has one slot; installing another repository replaces it, and the
// replaced value lives on only in the backup named by BackupPath.
type RuntimeSlot struct {
	Repo             string    `json:"repo"`
	InstalledAt      time.Time `json:"installed_at,omitzero"`
	ConfigPath       string    `json:"config_path,omitempty"`
	BackupPath       string    `json:"backup_path,omitempty"`
	ValueFingerprint string    `json:"value_fingerprint,omitempty"`

	// BackupContainsKey records that the backup taken by this install already held
	// this repository's plaintext key, so a later purge can delete it without
	// decrypting anything.
	BackupContainsKey bool `json:"backup_contains_key"`

	// PreviousModelProvider is the Codex `model_provider` value this install replaced;
	// nil means there was none and purge should delete the line instead of restoring.
	PreviousModelProvider *string `json:"previous_model_provider"`
}

// State is the versioned content of $OMC_HOME/state.json.
//
// It is an index, not the only truth: key and envelope paths are deterministic, so a
// lost state file costs display information, not the ability to use a key.
type State struct {
	Version  int                     `json:"version"`
	Repos    map[string]RepoState    `json:"repos"`
	Runtimes map[Runtime]RuntimeSlot `json:"runtimes"`
}

// emptyState is a state document with no records, at the current schema version.
func emptyState() State {
	return State{
		Version:  STATE_VERSION,
		Repos:    map[string]RepoState{},
		Runtimes: map[Runtime]RuntimeSlot{},
	}
}

// Config is the versioned content of $OMC_HOME/config.json. It holds only the
// normalized gateway root, never a key.
type Config struct {
	Version   int       `json:"version"`
	BaseURL   string    `json:"base_url"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`
}

// BaseURL is a resolved gateway root together with where it came from.
type BaseURL struct {
	// Root is the normalized gateway root: no trailing slash, no trailing /v1.
	Root string
	// Source is "flag", "env" or "config".
	Source string
	// Insecure is true for a plain http URL that is not on the loopback interface;
	// the caller warns instead of refusing, because a private Docker network
	// address is a normal deployment.
	Insecure bool
}

// KeyPair is an X25519 identity. PublicKey is the text form a caller submits with
// repo_key_apply ("x25519:<base64>"); Fingerprint is its stable, non-secret identity.
type KeyPair struct {
	PrivateKey  []byte
	PublicKey   string
	Fingerprint string
}

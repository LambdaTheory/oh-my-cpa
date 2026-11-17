package keyclient

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/repokey"
)

const (
	// MAX_ACCEPT_BYTES bounds the stdin payload of `key accept`. An envelope is a few
	// hundred bytes; the limit exists so a mistaken pipe of a large file fails before it
	// is decoded, not after it is in memory.
	MAX_ACCEPT_BYTES = 1 << 20

	// ENVELOPE_VERSION is the only inner envelope schema this client understands. It
	// mirrors repokey's own constant, which is private because nothing outside that
	// package should compose envelopes.
	ENVELOPE_VERSION = 1

	// acceptUnwrapDepth is how many JSON-string layers the input may hide behind: the
	// capability envelope holds an object, but a transport that serializes its argument
	// as text hands over a string containing the same object. Deeper nesting is refused
	// rather than followed, because it would only come from a malformed or hostile pipe.
	acceptUnwrapDepth = 2
)

// refusalCodePattern is how a refusal code from an MCP error envelope is echoed: only
// its shape, never any other part of the body, so a proxy's error page cannot reach the
// terminal through this path.
var refusalCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// AcceptedEnvelope is a validated delivery envelope together with the facts about the
// application it came from. InnerJSON is the compact envelope that is stored on disk; the
// plaintext it opens to is never part of this value.
type AcceptedEnvelope struct {
	InnerJSON     string
	ApplicationID string
	Kind          string
	KeyMask       string
	Algorithm     string
	// DecidedAt is the decision time the input carried, when it carried one. The
	// ciphertext result does not have to include it, so a zero value means "not stated",
	// not "never decided".
	DecidedAt time.Time
}

// ParseCiphertextResult reads what the MCP tool `repo_key_ciphertext` returned.
//
// Two shapes are accepted, because both reach a person's terminal: the capability
// envelope (`{"status":"success","data":{...}}`) and the bare `data` object. A bare
// envelope (`{"v":1,"alg":...}`) is accepted too, so a caller who saved the ciphertext
// field alone can still store it. Whatever the shape, the result is the same validated
// inner envelope and nothing about the input is echoed back.
func ParseCiphertextResult(input []byte) (AcceptedEnvelope, error) {
	if len(input) > MAX_ACCEPT_BYTES {
		return AcceptedEnvelope{}, Errorf(CODE_ENVELOPE_INVALID, "the input is larger than %d bytes; pass the result of repo_key_ciphertext, not a saved response body", MAX_ACCEPT_BYTES)
	}
	object, err := decodeJSONLayer(input, acceptUnwrapDepth)
	if err != nil {
		return AcceptedEnvelope{}, Errorf(CODE_ENVELOPE_INVALID, "the input is not a JSON object; pipe the result of repo_key_ciphertext on stdin")
	}
	if refusal := refusalDetail(object); refusal != "" {
		return AcceptedEnvelope{}, Errorf(CODE_ENVELOPE_INVALID, "repo_key_ciphertext did not return a ciphertext: %s", refusal)
	}
	for _, key := range []string{"data", "structuredContent"} {
		value, ok := object[key]
		if !ok {
			continue
		}
		inner, err := decodeJSONLayer(value, acceptUnwrapDepth-1)
		if err != nil {
			return AcceptedEnvelope{}, Errorf(CODE_ENVELOPE_INVALID, "the %q field of the input is not a JSON object", key)
		}
		return parseCiphertextObject(inner)
	}
	return parseCiphertextObject(object)
}

// parseCiphertextObject reads one object that is either a ciphertext result or a bare
// envelope.
func parseCiphertextObject(object map[string]json.RawMessage) (AcceptedEnvelope, error) {
	// A bare envelope carries epk/v; a result object carries ciphertext beside the
	// application facts. The envelope is checked first because it also has a ciphertext.
	if _, ok := object["epk"]; ok {
		envelope, compact, err := parseInnerEnvelope(object)
		if err != nil {
			return AcceptedEnvelope{}, err
		}
		return AcceptedEnvelope{InnerJSON: compact, Algorithm: envelope.Algorithm}, nil
	}
	algorithm := jsonString(object, "algorithm")
	if algorithm != "" && algorithm != repokey.Algorithm {
		return AcceptedEnvelope{}, Errorf(CODE_ENVELOPE_INVALID, "the envelope uses algorithm %q; this client reads %q", algorithm, repokey.Algorithm)
	}
	ciphertextRaw, ok := object["ciphertext"]
	if !ok {
		return AcceptedEnvelope{}, Errorf(CODE_ENVELOPE_INVALID, "the input has neither a ciphertext nor a bare envelope; pipe the result of repo_key_ciphertext on stdin")
	}
	inner, err := decodeJSONLayer(ciphertextRaw, 1)
	if err != nil {
		return AcceptedEnvelope{}, Errorf(CODE_ENVELOPE_INVALID, "the ciphertext field is not an envelope object")
	}
	envelope, compact, err := parseInnerEnvelope(inner)
	if err != nil {
		return AcceptedEnvelope{}, err
	}
	accepted := AcceptedEnvelope{
		InnerJSON:     compact,
		ApplicationID: jsonString(object, "application_id"),
		Kind:          jsonString(object, "kind"),
		KeyMask:       jsonString(object, "key_mask"),
		Algorithm:     envelope.Algorithm,
	}
	accepted.DecidedAt = jsonMilliseconds(object, "decided_at_ms")
	return accepted, nil
}

// parseInnerEnvelope validates the version and algorithm of an inner envelope and returns
// its compact form. Fields this client does not know are dropped rather than stored, so an
// envelope file only ever holds what the decryption path will read.
func parseInnerEnvelope(object map[string]json.RawMessage) (repokey.Envelope, string, error) {
	var envelope repokey.Envelope
	raw, err := json.Marshal(object)
	if err != nil {
		return repokey.Envelope{}, "", Errorf(CODE_ENVELOPE_INVALID, "cannot read the envelope object")
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return repokey.Envelope{}, "", Errorf(CODE_ENVELOPE_INVALID, "the envelope is not valid JSON")
	}
	if envelope.Version != ENVELOPE_VERSION {
		return repokey.Envelope{}, "", Errorf(CODE_ENVELOPE_INVALID, "the envelope version is %d; this client reads version %d", envelope.Version, ENVELOPE_VERSION)
	}
	if envelope.Algorithm != repokey.Algorithm {
		return repokey.Envelope{}, "", Errorf(CODE_ENVELOPE_INVALID, "the envelope algorithm is %q; this client reads %q", envelope.Algorithm, repokey.Algorithm)
	}
	if envelope.EphemeralPublicKey == "" || envelope.Nonce == "" || envelope.Ciphertext == "" {
		return repokey.Envelope{}, "", Errorf(CODE_ENVELOPE_INVALID, "the envelope is missing its ephemeral key, nonce or ciphertext")
	}
	compact, err := json.Marshal(envelope)
	if err != nil {
		return repokey.Envelope{}, "", Errorf(CODE_ENVELOPE_INVALID, "cannot encode the envelope")
	}
	return envelope, string(compact), nil
}

// refusalDetail names why an MCP error envelope carries no ciphertext. The code is
// echoed because it is the stable part a caller acts on; the detail message is not, and
// neither is anything else in the body.
func refusalDetail(object map[string]json.RawMessage) string {
	status := jsonString(object, "status")
	if status != "error" && status != "pending" && status != "rejected" && status != "uncertain" {
		return ""
	}
	if status != "error" {
		return "the answer was " + status
	}
	code := jsonString(object, "code")
	if refusalCodePattern.MatchString(code) {
		return "the answer was an error (" + code + ")"
	}
	return "the answer was an error"
}

// decodeJSONLayer decodes one JSON object, following at most depth string layers. A JSON
// string is decoded as text and read again, so a value that travelled as a quoted string
// reaches the same parser as one that travelled as an object.
func decodeJSONLayer(raw []byte, depth int) (map[string]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, errors.New("empty")
	}
	if trimmed[0] == '"' {
		if depth <= 0 {
			return nil, errors.New("string")
		}
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return nil, err
		}
		return decodeJSONLayer([]byte(text), depth-1)
	}
	if trimmed[0] != '{' {
		return nil, errors.New("not an object")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &object); err != nil {
		return nil, err
	}
	return object, nil
}

// jsonString reads one string field, treating a missing or non-string value as empty.
func jsonString(object map[string]json.RawMessage, key string) string {
	raw, ok := object[key]
	if !ok {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

// jsonMilliseconds reads one epoch-millisecond field, which is how MCP results carry time.
func jsonMilliseconds(object map[string]json.RawMessage, key string) time.Time {
	raw, ok := object[key]
	if !ok {
		return time.Time{}
	}
	var milliseconds int64
	if json.Unmarshal(raw, &milliseconds) != nil || milliseconds <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(milliseconds).UTC()
}

// ValueFingerprint is the stored identity of a plaintext key: "sha256:" followed by the
// base64url digest. It lets purge decide whether a configured value or a backup is this
// repository's key without keeping the key itself. The key is high-entropy random, so a
// plain digest is not a guessable oracle.
func ValueFingerprint(plaintext string) string {
	digest := sha256.Sum256([]byte(plaintext))
	return "sha256:" + base64.RawURLEncoding.EncodeToString(digest[:])
}

// AcceptOptions is one `key accept` run.
type AcceptOptions struct {
	Home Home
	// Repo is the repository identity as the person wrote it.
	Repo string
	// ApplicationID overrides the id in the input when the caller names one.
	ApplicationID string
	Input         []byte
	Now           time.Time
}

// AcceptResult reports what was stored.
type AcceptResult struct {
	Record       RepoState
	Accepted     AcceptedEnvelope
	EnvelopePath string
	// Replaced is true when an earlier envelope for the same repository was overwritten.
	Replaced bool
}

// Accept validates a delivery envelope, proves the local private key opens it, and only
// then stores it.
//
// Decrypting before writing is the point of the command: an envelope registered against
// another machine's public key would otherwise be stored happily and fail much later,
// inside `key exec`, with no clue about which step went wrong. The plaintext stays in this
// function's memory and only its fingerprint is recorded.
func Accept(options AcceptOptions) (AcceptResult, error) {
	repoInput := strings.TrimSpace(options.Repo)
	if repoInput == "" {
		return AcceptResult{}, Errorf(CODE_REPO_REQUIRED, "no repository; pass --repo <org/repo>")
	}
	repoID, display, err := DeriveRepoID(repoInput)
	if err != nil {
		return AcceptResult{}, err
	}
	accepted, err := ParseCiphertextResult(options.Input)
	if err != nil {
		return AcceptResult{}, err
	}
	if override := strings.TrimSpace(options.ApplicationID); override != "" {
		accepted.ApplicationID = override
	}
	now := options.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	home := options.Home
	privateKey, err := ReadPrivateKey(home, repoID)
	if err != nil {
		return AcceptResult{}, err
	}
	plaintext, err := repokey.Decrypt(privateKey, accepted.InnerJSON)
	if err != nil {
		if errors.Is(err, repokey.ErrDecryptFailed) {
			return AcceptResult{}, Errorf(CODE_ENVELOPE_MISMATCH, "the envelope does not open with the private key for %s; nothing was stored. Either the public key registered with the application is not this key, or the envelope is damaged", repoID)
		}
		return AcceptResult{}, Errorf(CODE_ENVELOPE_INVALID, "the envelope cannot be opened: its ephemeral key, nonce or ciphertext is malformed")
	}
	store, err := OpenStoreForUpdate(home, 0)
	if err != nil {
		return AcceptResult{}, err
	}
	defer store.Close()

	envelopeRelative, err := home.EnvelopeRelPath(repoID)
	if err != nil {
		return AcceptResult{}, err
	}
	envelopePath, err := home.EnvelopePath(repoID)
	if err != nil {
		return AcceptResult{}, err
	}
	replaced := false
	if _, statErr := os.Stat(envelopePath); statErr == nil {
		replaced = true
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return AcceptResult{}, Wrapf(statErr, CODE_ENVELOPE_INVALID, "cannot inspect %s: %v", envelopePath, statErr)
	}
	// The same atomic write state.json uses: a temporary file in the same directory,
	// flushed, narrowed to the owner, then renamed. A reader never sees a partial
	// envelope, and the file is never briefly world-readable under its final name.
	if err := writeFileAtomic(envelopePath, []byte(accepted.InnerJSON)); err != nil {
		// writeFileAtomic renames only after the content is complete, so a failure here
		// leaves the previous envelope (or none) in place and state untouched.
		return AcceptResult{}, err
	}

	record, exists := store.Repo(repoID)
	if !exists {
		pair, pairErr := KeyPairFromPrivate(privateKey)
		if pairErr != nil {
			return AcceptResult{}, pairErr
		}
		record = RepoState{Repo: repoID, RepoDisplay: display, CreatedAt: now}
		record.PublicKey = pair.PublicKey
		record.Fingerprint = pair.Fingerprint
	}
	if strings.TrimSpace(record.RepoDisplay) == "" {
		record.RepoDisplay = display
	}
	keyRelative, err := home.KeyRelPath(repoID)
	if err != nil {
		return AcceptResult{}, err
	}
	record.PrivateKeyFile = keyRelative
	if record.PublicKey == "" || record.Fingerprint == "" {
		pair, pairErr := KeyPairFromPrivate(privateKey)
		if pairErr != nil {
			return AcceptResult{}, pairErr
		}
		record.PublicKey = pair.PublicKey
		record.Fingerprint = pair.Fingerprint
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	if accepted.ApplicationID != "" {
		record.ApplicationID = accepted.ApplicationID
	}
	if accepted.Kind != "" {
		record.ApplicationKind = accepted.Kind
	}
	if accepted.KeyMask != "" {
		record.KeyMask = accepted.KeyMask
	}
	record.ApprovalStatus = APPROVAL_APPROVED
	record.StatusSource = STATUS_SOURCE_ACCEPT
	record.StatusUpdatedAt = now
	if !accepted.DecidedAt.IsZero() {
		record.DecidedAt = accepted.DecidedAt
	}
	record.EnvelopeFile = envelopeRelative
	record.EnvelopeReceivedAt = now
	record.EnvelopeAlgorithm = accepted.Algorithm
	record.ValueFingerprint = ValueFingerprint(plaintext)
	if err := store.PutRepo(record); err != nil {
		return AcceptResult{}, err
	}
	if err := store.Save(); err != nil {
		return AcceptResult{}, err
	}
	return AcceptResult{Record: record, Accepted: accepted, EnvelopePath: envelopePath, Replaced: replaced}, nil
}

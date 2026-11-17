package keyclient

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/repokey"
)

// ciphertextResult builds the MCP answer `repo_key_ciphertext` gives for a repository's
// own public key, so every accept test starts from a real envelope rather than a fixture
// that could drift from the encryption the gateway performs.
func ciphertextResult(t *testing.T, home Home, repoID, applicationID, plaintext string) (map[string]any, repokey.Envelope) {
	t.Helper()
	privateKey, err := ReadPrivateKey(home, repoID)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := KeyPairFromPrivate(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	publicBytes, err := repokey.ParsePublicKey(pair.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := repokey.Encrypt(publicBytes, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	var envelope repokey.Envelope
	if err := json.Unmarshal([]byte(sealed), &envelope); err != nil {
		t.Fatal(err)
	}
	return map[string]any{
		"application_id": applicationID,
		"kind":           "issue",
		"key_mask":       "sk-cpa-ab***wxyz",
		"algorithm":      repokey.Algorithm,
		"ciphertext":     sealed,
	}, envelope
}

// mustJSON is the compact form of a test fixture, the way it reaches a shell pipeline.
func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

// prepareAcceptedHome generates a repository key and returns the home and the MCP answer
// whose envelope that key opens.
func prepareAcceptedHome(t *testing.T, repoInput, applicationID, plaintext string) (Home, map[string]any) {
	t.Helper()
	home := newTestHome(t)
	if _, err := Generate(GenOptions{Home: home, Repo: repoInput, Now: time.Now()}); err != nil {
		t.Fatal(err)
	}
	repoID, _, err := DeriveRepoID(repoInput)
	if err != nil {
		t.Fatal(err)
	}
	result, _ := ciphertextResult(t, home, repoID, applicationID, plaintext)
	return home, result
}

func TestParseCiphertextResultAcceptsBothShapes(t *testing.T) {
	repo := "org/repo"
	inner := `{"v":1,"alg":"` + repokey.Algorithm + `","epk":"AAAA","nonce":"BBBB","ciphertext":"CCCC"}`
	bare := map[string]any{"v": 1, "alg": repokey.Algorithm, "epk": "AAAA", "nonce": "BBBB", "ciphertext": "CCCC"}
	data := map[string]any{
		"application_id": "0f0e0d0c", "kind": "issue", "key_mask": "sk-cpa-ab***wxyz",
		"algorithm": repokey.Algorithm, "ciphertext": inner,
	}
	envelope := map[string]any{"status": "success", "data": data}

	cases := map[string]any{
		"capability envelope": envelope,
		"bare data":           data,
		"bare envelope":       bare,
		"text content":        mustJSONString(t, data),
		"string ciphertext only": map[string]any{
			"application_id": "0f0e0d0c", "key_mask": "sk-cpa-ab***wxyz",
			"algorithm": repokey.Algorithm, "ciphertext": inner,
		},
	}
	for name, input := range cases {
		accepted, err := ParseCiphertextResult(mustJSON(t, input))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if accepted.Algorithm != repokey.Algorithm {
			t.Fatalf("%s: algorithm = %q", name, accepted.Algorithm)
		}
		var decoded repokey.Envelope
		if err := json.Unmarshal([]byte(accepted.InnerJSON), &decoded); err != nil {
			t.Fatalf("%s: inner envelope is not JSON: %v", name, err)
		}
		if decoded.Version != 1 || decoded.EphemeralPublicKey != "AAAA" {
			t.Fatalf("%s: inner envelope = %+v", name, decoded)
		}
		_ = repo
	}
}

// mustJSONString is how an MCP text content field carries the same object: a JSON string
// containing JSON.
func mustJSONString(t *testing.T, value any) string {
	t.Helper()
	encoded := mustJSON(t, value)
	quoted, err := json.Marshal(string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	return string(quoted)
}

func TestParseCiphertextResultRejectsBadAlgorithmAndVersion(t *testing.T) {
	inner := map[string]any{"v": 1, "alg": "aes-gcm-other", "epk": "AAAA", "nonce": "BBBB", "ciphertext": "CCCC"}
	badAlgorithm := map[string]any{
		"algorithm": "aes-gcm-other", "ciphertext": string(mustJSON(t, inner)),
	}
	if _, err := ParseCiphertextResult(mustJSON(t, badAlgorithm)); CodeOf(err) != CODE_ENVELOPE_INVALID {
		t.Fatalf("outer algorithm: code = %q (err=%v)", CodeOf(err), err)
	}
	// The outer algorithm can be right while the inner one is wrong; the inner one is what
	// the decryption path reads, so it must be checked on its own.
	badInner := map[string]any{"algorithm": repokey.Algorithm, "ciphertext": string(mustJSON(t, inner))}
	if _, err := ParseCiphertextResult(mustJSON(t, badInner)); CodeOf(err) != CODE_ENVELOPE_INVALID {
		t.Fatalf("inner algorithm: code = %q (err=%v)", CodeOf(err), err)
	}
	wrongVersion := map[string]any{"v": 2, "alg": repokey.Algorithm, "epk": "AAAA", "nonce": "BBBB", "ciphertext": "CCCC"}
	if _, err := ParseCiphertextResult(mustJSON(t, wrongVersion)); CodeOf(err) != CODE_ENVELOPE_INVALID {
		t.Fatalf("version: code = %q (err=%v)", CodeOf(err), err)
	}
	// The outer object can also be the delivery result rather than the envelope, and a
	// wrong outer algorithm is refused before the inner one is even parsed.
	if _, err := ParseCiphertextResult([]byte(`{"algorithm":"aes-gcm-other","ciphertext":{"v":1}}`)); CodeOf(err) != CODE_ENVELOPE_INVALID {
		t.Fatalf("outer object algorithm: code = %q (err=%v)", CodeOf(err), err)
	}
}

func TestParseCiphertextResultRejectsInputWithoutCiphertext(t *testing.T) {
	for name, input := range map[string]string{
		"not json":        `not a json body`,
		"error envelope":  `{"status":"error","code":"repo_key_not_found"}`,
		"empty data":      `{"status":"success","data":{}}`,
		"empty body":      ``,
		"a bare array":    `[]`,
		"too deep string": `"\"{\\\"v\\\":1}\""`,
	} {
		_, err := ParseCiphertextResult([]byte(input))
		if CodeOf(err) != CODE_ENVELOPE_INVALID {
			t.Fatalf("%s: code = %q (err=%v)", name, CodeOf(err), err)
		}
	}
}

func TestParseCiphertextResultRefusesMoreThanOneMiB(t *testing.T) {
	oversized := `{"ciphertext":"` + strings.Repeat("A", MAX_ACCEPT_BYTES) + `"}`
	_, err := ParseCiphertextResult([]byte(oversized))
	if CodeOf(err) != CODE_ENVELOPE_INVALID {
		t.Fatalf("code = %q (err=%v)", CodeOf(err), err)
	}
	if !strings.Contains(MessageOf(err), "larger than") {
		t.Fatalf("the refusal should say the input is too large: %q", MessageOf(err))
	}
}

func TestAcceptStoresEnvelopeAndRecordsTheApplication(t *testing.T) {
	home, result := prepareAcceptedHome(t, "Org/Repo", "0f0e0d0c", "sk-cpa-plaintext")
	at := time.Date(2026, 11, 17, 9, 12, 0, 0, time.UTC)
	accepted, err := Accept(AcceptOptions{Home: home, Repo: "org/repo", Input: mustJSON(t, result), Now: at})
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Replaced {
		t.Fatal("the first accept replaced something")
	}
	record := accepted.Record
	if record.ApplicationID != "0f0e0d0c" || record.ApplicationKind != APPLICATION_KIND_ISSUE {
		t.Fatalf("application = %q/%q", record.ApplicationID, record.ApplicationKind)
	}
	if record.ApprovalStatus != APPROVAL_APPROVED || record.StatusSource != STATUS_SOURCE_ACCEPT {
		t.Fatalf("approval = %q/%q", record.ApprovalStatus, record.StatusSource)
	}
	if !record.StatusUpdatedAt.Equal(at) || !record.EnvelopeReceivedAt.Equal(at) {
		t.Fatalf("times = %v/%v", record.StatusUpdatedAt, record.EnvelopeReceivedAt)
	}
	if record.KeyMask != "sk-cpa-ab***wxyz" || record.EnvelopeAlgorithm != repokey.Algorithm {
		t.Fatalf("record = %+v", record)
	}
	// The fingerprint identifies the plaintext; the plaintext itself must not be stored.
	if want := ValueFingerprint("sk-cpa-plaintext"); record.ValueFingerprint != want {
		t.Fatalf("value fingerprint = %q, want %q", record.ValueFingerprint, want)
	}
	stored, err := os.ReadFile(home.StatePath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored), "sk-cpa-plaintext") || strings.Contains(string(stored), "sk-cpa-plainte") {
		t.Fatalf("state.json holds the plaintext key:\n%s", stored)
	}

	envelopePath, err := home.EnvelopePath("org/repo")
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := os.ReadFile(envelopePath)
	if err != nil {
		t.Fatal(err)
	}
	var decoded repokey.Envelope
	if err := json.Unmarshal(envelope, &decoded); err != nil {
		t.Fatalf("the stored envelope is not valid: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(home.Root, filepath.FromSlash(record.PrivateKeyFile)))
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := repokey.Decrypt(raw, string(envelope))
	if err != nil {
		t.Fatalf("the stored envelope does not open: %v", err)
	}
	if plaintext != "sk-cpa-plaintext" {
		t.Fatalf("decrypted %q", plaintext)
	}
}

// TestAcceptRefusesAnEnvelopeForAnotherKey is the reason accept decrypts before writing:
// an envelope for a different key must not reach the disk at all.
func TestAcceptRefusesAnEnvelopeForAnotherKey(t *testing.T) {
	home := newTestHome(t)
	if _, err := Generate(GenOptions{Home: home, Repo: "org/repo", Now: time.Now()}); err != nil {
		t.Fatal(err)
	}
	otherPair, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	otherPublic, err := repokey.ParsePublicKey(otherPair.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := repokey.Encrypt(otherPublic, "sk-cpa-someone-else")
	if err != nil {
		t.Fatal(err)
	}
	input := mustJSON(t, map[string]any{"ciphertext": foreign, "algorithm": repokey.Algorithm})
	_, err = Accept(AcceptOptions{Home: home, Repo: "org/repo", Input: input, Now: time.Now()})
	if CodeOf(err) != CODE_ENVELOPE_MISMATCH {
		t.Fatalf("code = %q (err=%v)", CodeOf(err), err)
	}
	envelopePath, err := home.EnvelopePath("org/repo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(envelopePath); !os.IsNotExist(err) {
		t.Fatalf("a mismatched envelope was written (err=%v)", err)
	}
	store, err := OpenStore(home)
	if err != nil {
		t.Fatal(err)
	}
	if record, ok := store.Repo("org/repo"); !ok || record.EnvelopeFile != "" || record.ApprovalStatus != APPROVAL_NOT_APPLIED {
		t.Fatalf("a mismatched envelope changed state: %+v (ok=%v)", record, ok)
	}
}

func TestAcceptWithoutAPrivateKeyIsKeyNotFound(t *testing.T) {
	home := newTestHome(t)
	wellFormed := `{"v":1,"alg":"` + repokey.Algorithm + `","epk":"AAAA","nonce":"BBBB","ciphertext":"CCCC"}`
	_, err := Accept(AcceptOptions{Home: home, Repo: "org/repo", Input: []byte(wellFormed), Now: time.Now()})
	if CodeOf(err) != CODE_KEY_NOT_FOUND {
		t.Fatalf("code = %q (err=%v)", CodeOf(err), err)
	}
	// An input that is not an envelope at all is refused before the key is even looked
	// for, so a caller fixing the pipe does not also have to create a key.
	if _, err := Accept(AcceptOptions{Home: home, Repo: "org/repo", Input: []byte(`{"ciphertext":"{}"}`), Now: time.Now()}); CodeOf(err) != CODE_ENVELOPE_INVALID {
		t.Fatalf("a malformed envelope: code = %q (err=%v)", CodeOf(err), err)
	}
}

func TestAcceptReplacesThePreviousEnvelope(t *testing.T) {
	home, first := prepareAcceptedHome(t, "org/repo", "first-app", "sk-cpa-first")
	if _, err := Accept(AcceptOptions{Home: home, Repo: "org/repo", Input: mustJSON(t, first), Now: time.Now()}); err != nil {
		t.Fatal(err)
	}
	second, _ := ciphertextResult(t, home, "org/repo", "second-app", "sk-cpa-second")
	second["key_mask"] = "sk-cpa-zz***zzzz"
	accepted, err := Accept(AcceptOptions{Home: home, Repo: "org/repo", Input: mustJSON(t, second), Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if !accepted.Replaced {
		t.Fatal("a second accept should report that it replaced the envelope")
	}
	if accepted.Record.ApplicationID != "second-app" || accepted.Record.KeyMask != "sk-cpa-zz***zzzz" {
		t.Fatalf("record = %+v", accepted.Record)
	}
	if want := ValueFingerprint("sk-cpa-second"); accepted.Record.ValueFingerprint != want {
		t.Fatalf("value fingerprint = %q, want %q", accepted.Record.ValueFingerprint, want)
	}
}

// TestAcceptExplicitApplicationIDWins covers a result that omits the id: the caller's
// --application-id names the application the envelope belongs to.
func TestAcceptExplicitApplicationIDWins(t *testing.T) {
	home, result := prepareAcceptedHome(t, "org/repo", "", "sk-cpa-plaintext")
	delete(result, "application_id")
	accepted, err := Accept(AcceptOptions{Home: home, Repo: "org/repo", ApplicationID: "named-app", Input: mustJSON(t, result), Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Record.ApplicationID != "named-app" {
		t.Fatalf("application id = %q", accepted.Record.ApplicationID)
	}
}

// TestAcceptRebuildsALostStateRecord: the state file is an index, so a key and envelope
// that exist on disk must still produce a record.
func TestAcceptRebuildsALostStateRecord(t *testing.T) {
	home, result := prepareAcceptedHome(t, "org/repo", "app", "sk-cpa-plaintext")
	if err := os.Remove(home.StatePath()); err != nil {
		t.Fatal(err)
	}
	accepted, err := Accept(AcceptOptions{Home: home, Repo: "org/repo", Input: mustJSON(t, result), Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Record.PublicKey == "" || accepted.Record.PrivateKeyFile == "" {
		t.Fatalf("the rebuilt record is missing the key identity: %+v", accepted.Record)
	}
}

func TestAcceptedEnvelopeFileIsOwnerOnlyAndAtomic(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Windows build asserts ACLs in home_windows_test.go; the 0600 claim is Unix")
	}
	home, result := prepareAcceptedHome(t, "org/repo", "app", "sk-cpa-plaintext")
	if _, err := Accept(AcceptOptions{Home: home, Repo: "org/repo", Input: mustJSON(t, result), Now: time.Now()}); err != nil {
		t.Fatal(err)
	}
	envelopePath, err := home.EnvelopePath("org/repo")
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(envelopePath)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != FILE_MODE {
		t.Fatalf("envelope mode = %04o, want %04o", mode, FILE_MODE)
	}
	// No temporary file from the atomic write may survive beside it.
	entries, err := os.ReadDir(home.EnvelopesDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp-") {
			t.Fatalf("a temporary file was left behind: %s", entry.Name())
		}
	}
}

package keyclient

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/repokey"
)

func TestGenerateKeyPairProducesAUsableIdentity(t *testing.T) {
	pair, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if len(pair.PrivateKey) != PRIVATE_KEY_BYTES {
		t.Fatalf("private key length = %d, want %d", len(pair.PrivateKey), PRIVATE_KEY_BYTES)
	}
	if !strings.HasPrefix(pair.PublicKey, "x25519:") {
		t.Fatalf("public key = %q, want an x25519: prefix", pair.PublicKey)
	}
	// The printed key must be exactly what the server parses back out of an apply call.
	publicBytes, err := repokey.ParsePublicKey(pair.PublicKey)
	if err != nil {
		t.Fatalf("the printed public key does not parse: %v", err)
	}
	if want := repokey.PublicKeyFingerprint(publicBytes); pair.Fingerprint != want {
		t.Fatalf("fingerprint = %q, want %q", pair.Fingerprint, want)
	}
	// And the private half must open what the public half seals.
	envelope, err := repokey.Encrypt(publicBytes, "sk-cpa-test")
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := repokey.Decrypt(pair.PrivateKey, envelope)
	if err != nil {
		t.Fatalf("the generated pair cannot open its own envelope: %v", err)
	}
	if plaintext != "sk-cpa-test" {
		t.Fatalf("decrypted %q", plaintext)
	}
}

func TestWriteAndReadPrivateKey(t *testing.T) {
	home := newTestHome(t)
	pair, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	path, err := WritePrivateKey(home, "org/repo", pair.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != PRIVATE_KEY_BYTES {
		t.Fatalf("the file holds %d bytes, want the raw %d-byte key and nothing else", len(raw), PRIVATE_KEY_BYTES)
	}
	reloaded, err := ReadPrivateKey(home, "org/repo")
	if err != nil {
		t.Fatal(err)
	}
	if string(reloaded) != string(pair.PrivateKey) {
		t.Fatal("the reloaded key differs from the written one")
	}
}

func TestWritePrivateKeyNeverOverwrites(t *testing.T) {
	home := newTestHome(t)
	first := make([]byte, PRIVATE_KEY_BYTES)
	first[0] = 1
	if _, err := WritePrivateKey(home, "org/repo", first); err != nil {
		t.Fatal(err)
	}
	second := make([]byte, PRIVATE_KEY_BYTES)
	second[0] = 2
	_, err := WritePrivateKey(home, "org/repo", second)
	if CodeOf(err) != CODE_KEY_EXISTS {
		t.Fatalf("code = %q, want %q (err=%v)", CodeOf(err), CODE_KEY_EXISTS, err)
	}
	stored, err := ReadPrivateKey(home, "org/repo")
	if err != nil {
		t.Fatal(err)
	}
	if stored[0] != 1 {
		t.Fatal("the existing key was replaced")
	}
}

func TestReadPrivateKeyReportsMissingAndMalformed(t *testing.T) {
	home := newTestHome(t)
	if _, err := ReadPrivateKey(home, "org/repo"); CodeOf(err) != CODE_KEY_NOT_FOUND {
		t.Fatalf("missing: code = %q, want %q", CodeOf(err), CODE_KEY_NOT_FOUND)
	}
	path, err := home.KeyPath("org/repo")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("too short"), FILE_MODE); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPrivateKey(home, "org/repo"); CodeOf(err) != CODE_KEY_UNREADABLE {
		t.Fatalf("short: code = %q, want %q", CodeOf(err), CODE_KEY_UNREADABLE)
	}
}

func TestGenerateCreatesThenReuses(t *testing.T) {
	home := newTestHome(t)
	at := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)

	first, err := Generate(GenOptions{Home: home, Repo: "org/repo", Now: at})
	if err != nil {
		t.Fatal(err)
	}
	if first.Reused {
		t.Fatal("the first run should have created a key")
	}
	if first.Record.ApprovalStatus != APPROVAL_NOT_APPLIED || first.Record.StatusSource != STATUS_SOURCE_GEN {
		t.Fatalf("record = %+v", first.Record)
	}
	if !first.Record.CreatedAt.Equal(at) {
		t.Fatalf("created_at = %v, want %v", first.Record.CreatedAt, at)
	}
	keyRelative, err := home.KeyRelPath("org/repo")
	if err != nil {
		t.Fatal(err)
	}
	if first.Record.PrivateKeyFile != keyRelative {
		t.Fatalf("private_key_file = %q, want %q", first.Record.PrivateKeyFile, keyRelative)
	}

	second, err := Generate(GenOptions{Home: home, Repo: "org/repo", Now: at.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if !second.Reused {
		t.Fatal("the second run should have reused the key")
	}
	if second.Pairs.PublicKey != first.Pairs.PublicKey {
		t.Fatalf("reuse produced a different public key:\n%s\n%s", first.Pairs.PublicKey, second.Pairs.PublicKey)
	}
	if !second.Record.CreatedAt.Equal(at) {
		t.Fatalf("reuse changed created_at to %v", second.Record.CreatedAt)
	}
	// Reuse must not touch the file either.
	stored, err := ReadPrivateKey(home, "org/repo")
	if err != nil {
		t.Fatal(err)
	}
	if string(stored) != string(first.Pairs.PrivateKey) {
		t.Fatal("reuse rewrote the private key")
	}
}

// TestGenerateRepairsStateFromAnExistingKey covers a state file that was lost or written
// by an older version: the private key is the truth, and the public identity is derived
// from it rather than generating a second key beside it.
func TestGenerateRepairsStateFromAnExistingKey(t *testing.T) {
	home := newTestHome(t)
	pair, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := WritePrivateKey(home, "org/repo", pair.PrivateKey); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStoreForUpdate(home, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutRepo(RepoState{Repo: "org/repo", RepoDisplay: "Org/Repo"}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.Save(); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	result, err := Generate(GenOptions{Home: home, Repo: "org/repo", Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Reused {
		t.Fatal("an existing key should be reused")
	}
	if result.Record.PublicKey != pair.PublicKey || result.Record.Fingerprint != pair.Fingerprint {
		t.Fatalf("the record was not repaired: %+v", result.Record)
	}
}

func TestGenerateWritesTheBaseURL(t *testing.T) {
	home := newTestHome(t)
	result, err := Generate(GenOptions{Home: home, Repo: "org/repo", BaseURL: "http://127.0.0.1:8317/v1"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.WroteConfig {
		t.Fatal("a --base-url run should have saved the address")
	}
	config, err := home.ReadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.BaseURL != "http://127.0.0.1:8317" {
		t.Fatalf("stored base URL = %q, want the normalized root", config.BaseURL)
	}
}

func TestGenerateRejectsAnInvalidBaseURL(t *testing.T) {
	home := newTestHome(t)
	_, err := Generate(GenOptions{Home: home, Repo: "org/repo", BaseURL: "http://127.0.0.1:8317/v1/messages"})
	if CodeOf(err) != CODE_BASE_URL_INVALID {
		t.Fatalf("code = %q, want %q (err=%v)", CodeOf(err), CODE_BASE_URL_INVALID, err)
	}
	if _, statErr := os.Stat(home.ConfigPath()); !os.IsNotExist(statErr) {
		t.Fatalf("an invalid base URL should not be written (err=%v)", statErr)
	}
}

func TestGenerateRequiresARepo(t *testing.T) {
	home := newTestHome(t)
	if _, err := Generate(GenOptions{Home: home}); CodeOf(err) != CODE_REPO_REQUIRED {
		t.Fatalf("code = %q, want %q", CodeOf(err), CODE_REPO_REQUIRED)
	}
}

// TestGenerateRegistersTheNormalizedIdentity checks that an SSH remote and a short form
// land on the same record, which is what lets two clones share one key.
func TestGenerateRegistersTheNormalizedIdentity(t *testing.T) {
	home := newTestHome(t)
	first, err := Generate(GenOptions{Home: home, Repo: "git@github.com:Org/Repo.git"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Generate(GenOptions{Home: home, Repo: "https://github.com/org/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Record.Repo != "org/repo" || second.Record.Repo != "org/repo" {
		t.Fatalf("identities = %q and %q, want both to be org/repo", first.Record.Repo, second.Record.Repo)
	}
	if !second.Reused {
		t.Fatal("the second spelling should have found the first key")
	}
	if second.Record.RepoDisplay != "Org/Repo" {
		t.Fatalf("display = %q; the first spelling should be kept", second.Record.RepoDisplay)
	}
}

func TestKeyAndEnvelopePathsAreSiblings(t *testing.T) {
	home := Home{Root: filepath.Join(t.TempDir(), "home")}
	keyPath, err := home.KeyPath("org/repo")
	if err != nil {
		t.Fatal(err)
	}
	envelopePath, err := home.EnvelopePath("org/repo")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(keyPath) != home.KeysDir() {
		t.Fatalf("key path %q is outside %q", keyPath, home.KeysDir())
	}
	if filepath.Dir(envelopePath) != home.EnvelopesDir() {
		t.Fatalf("envelope path %q is outside %q", envelopePath, home.EnvelopesDir())
	}
	if strings.TrimSuffix(filepath.Base(keyPath), ".key") != strings.TrimSuffix(filepath.Base(envelopePath), ".json") {
		t.Fatalf("key %q and envelope %q should share a stem", keyPath, envelopePath)
	}
}

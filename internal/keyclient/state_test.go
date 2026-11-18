package keyclient

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestHome(t *testing.T) Home {
	t.Helper()
	home := Home{Root: filepath.Join(t.TempDir(), "home")}
	if err := home.Ensure(); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestStoreRoundTrip(t *testing.T) {
	home := newTestHome(t)
	at := time.Date(2026, 11, 17, 9, 12, 0, 0, time.UTC)

	store, err := OpenStoreForUpdate(home, 0)
	if err != nil {
		t.Fatal(err)
	}
	record := RepoState{
		Repo:               "org/repo",
		RepoDisplay:        "Org/Repo",
		PrivateKeyFile:     "keys/org%2Frepo-1a2b3c4d.key",
		PublicKey:          "x25519:AAAA",
		Fingerprint:        "x25519:sha256:BBBB",
		CreatedAt:          at,
		ApplicationID:      "0f0e0d0c",
		ApplicationKind:    APPLICATION_KIND_ISSUE,
		ApprovalStatus:     APPROVAL_APPROVED,
		StatusSource:       STATUS_SOURCE_ACCEPT,
		StatusUpdatedAt:    at,
		KeyMask:            "sk-cpa-ab***wxyz",
		RepoState:          REPO_STATE_ACTIVE,
		EnvelopeFile:       "envelopes/org%2Frepo-1a2b3c4d.json",
		EnvelopeReceivedAt: at,
		EnvelopeAlgorithm:  "x25519-hkdf-sha256-aes256gcm",
	}
	if err := store.PutRepo(record); err != nil {
		store.Close()
		t.Fatal(err)
	}
	previous := "openai"
	if err := store.PutRuntime(RuntimeCodex, RuntimeSlot{
		Repo:                  "org/repo",
		InstalledAt:           at,
		ConfigPath:            "/home/user/.codex/config.toml",
		BackupPath:            "/home/user/.omc/backups/b.toml",
		ValueFingerprint:      "sha256:CCCC",
		PreviousModelProvider: &previous,
	}); err != nil {
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

	reopened, err := OpenStore(home)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	gotRepo, ok := reopened.Repo("org/repo")
	if !ok {
		t.Fatal("the repository record did not survive the round trip")
	}
	if gotRepo.PublicKey != record.PublicKey || gotRepo.KeyMask != record.KeyMask || gotRepo.StatusSource != record.StatusSource {
		t.Fatalf("round trip lost fields: %+v", gotRepo)
	}
	if !gotRepo.CreatedAt.Equal(at) {
		t.Fatalf("created_at = %v, want %v", gotRepo.CreatedAt, at)
	}
	gotSlot, ok := reopened.Runtime(RuntimeCodex)
	if !ok {
		t.Fatal("the runtime slot did not survive the round trip")
	}
	if gotSlot.PreviousModelProvider == nil || *gotSlot.PreviousModelProvider != previous {
		t.Fatalf("previous_model_provider = %v, want %q", gotSlot.PreviousModelProvider, previous)
	}
	if gotSlot.BackupContainsKey {
		t.Fatal("a slot with no backup key claim came back true")
	}
}

func TestStateUsesSnakeCaseWithoutZeroTimes(t *testing.T) {
	home := newTestHome(t)
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

	content, err := os.ReadFile(home.StatePath())
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, field := range []string{`"version"`, `"repos"`, `"runtimes"`, `"repo_display"`} {
		if !strings.Contains(text, field) {
			t.Fatalf("state.json is missing the %s field:\n%s", field, text)
		}
	}
	for _, absent := range []string{`"0001-01-01T00:00:00Z"`, `"RepoDisplay"`} {
		if strings.Contains(text, absent) {
			t.Fatalf("state.json should not contain %s:\n%s", absent, text)
		}
	}
	var raw map[string]any
	if err := json.Unmarshal(content, &raw); err != nil {
		t.Fatalf("state.json is not valid JSON: %v", err)
	}
	if version, ok := raw["version"].(float64); !ok || int(version) != STATE_VERSION {
		t.Fatalf("version = %v, want %d", raw["version"], STATE_VERSION)
	}

	// The record's field names are the stable schema other slices read, so an accidental
	// rename shows up here rather than in a consumer.
	repos, ok := raw["repos"].(map[string]any)
	if !ok || len(repos) != 1 {
		t.Fatalf("repos = %v, want one record", raw["repos"])
	}
	record, ok := repos["org/repo"].(map[string]any)
	if !ok {
		t.Fatalf("repos has no org/repo record: %v", repos)
	}
	allowed := map[string]bool{
		"repo": true, "repo_display": true, "private_key_file": true, "public_key": true,
		"public_key_fingerprint": true, "created_at": true, "application_id": true,
		"application_kind": true, "approval_status": true, "status_source": true,
		"status_updated_at": true, "decided_at": true, "key_mask": true, "value_fingerprint": true, "repo_state": true,
		"repo_state_updated_at": true, "envelope_file": true, "envelope_received_at": true,
		"envelope_algorithm": true,
	}
	for field := range record {
		if !allowed[field] {
			t.Fatalf("repo record has an undocumented field %q", field)
		}
	}
}

func TestOpenStoreOnAMissingFileIsEmpty(t *testing.T) {
	home := Home{Root: filepath.Join(t.TempDir(), "absent")}
	store, err := OpenStore(home)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if len(store.Repos()) != 0 {
		t.Fatalf("a missing state file should read as empty, got %v", store.Repos())
	}
	if _, err := os.Stat(home.StatePath()); !os.IsNotExist(err) {
		t.Fatalf("reading created state.json (err=%v)", err)
	}
}

func TestOpenStoreRejectsCorruptJSON(t *testing.T) {
	home := newTestHome(t)
	if err := os.WriteFile(home.StatePath(), []byte("{not json"), FILE_MODE); err != nil {
		t.Fatal(err)
	}
	_, err := OpenStore(home)
	if CodeOf(err) != CODE_STATE_CORRUPT {
		t.Fatalf("code = %q, want %q (err=%v)", CodeOf(err), CODE_STATE_CORRUPT, err)
	}
}

func TestOpenStoreRejectsAnUnknownVersion(t *testing.T) {
	home := newTestHome(t)
	if err := os.WriteFile(home.StatePath(), []byte(`{"version":99,"repos":{},"runtimes":{}}`), FILE_MODE); err != nil {
		t.Fatal(err)
	}
	_, err := OpenStore(home)
	if CodeOf(err) != CODE_STATE_CORRUPT {
		t.Fatalf("code = %q, want %q (err=%v)", CodeOf(err), CODE_STATE_CORRUPT, err)
	}
	if !strings.Contains(MessageOf(err), "99") {
		t.Fatalf("the message should name the version it found: %q", MessageOf(err))
	}
}

func TestOpenStoreRejectsAFutureField(t *testing.T) {
	home := newTestHome(t)
	if err := os.WriteFile(home.StatePath(), []byte(`{"version":1,"repos":{},"runtimes":{},"future":true}`), FILE_MODE); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(home); CodeOf(err) != CODE_STATE_CORRUPT {
		t.Fatalf("an unknown field should be refused so this binary cannot drop it, got %v", err)
	}
}

// TestCloseWithoutSaveLeavesTheFileUnchanged pins the read-modify-write contract: a
// command that changes records and then fails must not have published anything.
func TestCloseWithoutSaveLeavesTheFileUnchanged(t *testing.T) {
	home := newTestHome(t)
	store, err := OpenStoreForUpdate(home, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutRepo(RepoState{Repo: "org/one"}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.Save(); err != nil {
		store.Close()
		t.Fatal(err)
	}
	before, err := os.ReadFile(home.StatePath())
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.PutRepo(RepoState{Repo: "org/two"}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	after, err := os.ReadFile(home.StatePath())
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("closing without Save changed the file:\nbefore=%s\nafter=%s", before, after)
	}
	assertNoTemporaryFiles(t, home.Root)
}

// TestSaveIsAtomic is the concurrency claim: a reader of state.json never sees a partial
// document, because Save writes a temporary file and renames it into place. Without the
// rename a reader could parse a half-written record; this test fails against an
// in-place rewrite.
func TestSaveIsAtomic(t *testing.T) {
	home := newTestHome(t)
	seed, err := OpenStoreForUpdate(home, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.PutRepo(RepoState{Repo: "org/seed"}); err != nil {
		seed.Close()
		t.Fatal(err)
	}
	if err := seed.Save(); err != nil {
		seed.Close()
		t.Fatal(err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	writerDone := make(chan error, 1)
	go func() {
		defer close(writerDone)
		for run := 0; run < 12; run++ {
			store, err := OpenStoreForUpdate(home, 0)
			if err != nil {
				writerDone <- err
				return
			}
			repo := fmt.Sprintf("org/repo-%d", run)
			if err := store.PutRepo(RepoState{Repo: repo}); err != nil {
				store.Close()
				writerDone <- err
				return
			}
			if err := store.Save(); err != nil {
				store.Close()
				writerDone <- err
				return
			}
			if err := store.Close(); err != nil {
				writerDone <- err
				return
			}
		}
		close(stop)
	}()

	reads := 0
	for {
		select {
		case <-stop:
			if err := <-writerDone; err != nil {
				t.Fatal(err)
			}
			assertNoTemporaryFiles(t, home.Root)
			if reads == 0 {
				t.Fatal("the reader never ran")
			}
			return
		default:
		}
		content, err := os.ReadFile(home.StatePath())
		if err != nil {
			t.Fatalf("a read hit an unrenamed or missing file: %v", err)
		}
		var decoded State
		if err := json.Unmarshal(content, &decoded); err != nil {
			t.Fatalf("a read saw a partial document: %v\n%s", err, content)
		}
		if decoded.Version != STATE_VERSION {
			t.Fatalf("a read saw version %d: %s", decoded.Version, content)
		}
		reads++
	}
}

func assertNoTemporaryFiles(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp-") {
			t.Fatalf("a temporary file survived: %s", entry.Name())
		}
	}
}

// TestHomeLockSerializesWriters is the concurrency claim behind a per-home lock: a
// second writer waits instead of interleaving its read-modify-write with the first.
func TestHomeLockSerializesWriters(t *testing.T) {
	home := newTestHome(t)
	holder, err := AcquireHomeLock(home, 0)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	acquired := make(chan error, 1)
	go func() {
		close(started)
		_, err := AcquireHomeLock(home, 2*time.Second)
		acquired <- err
	}()
	<-started
	select {
	case err := <-acquired:
		holder.Release()
		t.Fatalf("the second writer did not wait for the first: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	if err := holder.Release(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-acquired:
		if err != nil {
			t.Fatalf("the second writer should have got the lock after release: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the second writer never got the lock")
	}
}

func TestHomeLockTimesOut(t *testing.T) {
	home := newTestHome(t)
	holder, err := AcquireHomeLock(home, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Release()
	_, err = AcquireHomeLock(home, 100*time.Millisecond)
	if CodeOf(err) != CODE_STATE_LOCKED {
		t.Fatalf("code = %q, want %q (err=%v)", CodeOf(err), CODE_STATE_LOCKED, err)
	}
}

func TestConfigRoundTrip(t *testing.T) {
	home := newTestHome(t)
	at := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	if err := home.WriteConfig("http://127.0.0.1:8317", at); err != nil {
		t.Fatal(err)
	}
	config, err := home.ReadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.BaseURL != "http://127.0.0.1:8317" || config.Version != CONFIG_VERSION {
		t.Fatalf("config = %+v", config)
	}
}

func TestReadConfigOnAMissingFileIsEmpty(t *testing.T) {
	home := Home{Root: filepath.Join(t.TempDir(), "absent")}
	config, err := home.ReadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.BaseURL != "" {
		t.Fatalf("base URL = %q, want empty", config.BaseURL)
	}
}

func TestReadConfigRejectsBadJSON(t *testing.T) {
	home := newTestHome(t)
	if err := os.WriteFile(home.ConfigPath(), []byte("nope"), FILE_MODE); err != nil {
		t.Fatal(err)
	}
	if _, err := home.ReadConfig(); CodeOf(err) != CODE_CONFIG_UNREADABLE {
		t.Fatalf("code = %q, want %q (err=%v)", CodeOf(err), CODE_CONFIG_UNREADABLE, err)
	}
}

func TestNormalizeBaseURL(t *testing.T) {
	accepted := map[string]string{
		"http://127.0.0.1:8317":           "http://127.0.0.1:8317",
		"http://127.0.0.1:8317/":          "http://127.0.0.1:8317",
		"http://127.0.0.1:8317/v1":        "http://127.0.0.1:8317",
		"https://omc.example.com/omc":     "https://omc.example.com/omc",
		"https://omc.example.com/omc/v1/": "https://omc.example.com/omc",
		" http://cli-proxy-api:8317 ":     "http://cli-proxy-api:8317",
		"http://127.0.0.1:8317/api":       "http://127.0.0.1:8317/api",
	}
	for input, want := range accepted {
		got, err := NormalizeBaseURL(input)
		if err != nil {
			t.Fatalf("NormalizeBaseURL(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("NormalizeBaseURL(%q) = %q, want %q", input, got, want)
		}
	}
	rejected := []string{
		"",
		"127.0.0.1:8317",
		"ftp://127.0.0.1:8317",
		"http://",
		"http://user:pass@127.0.0.1:8317",
		"http://127.0.0.1:8317?key=1",
		"http://127.0.0.1:8317#frag",
		"http://127.0.0.1:8317/v1/messages",
		"http://127.0.0.1:8317/v1/responses",
		"http://127.0.0.1:8317/omc/v1/messages",
	}
	for _, input := range rejected {
		if _, err := NormalizeBaseURL(input); err == nil {
			t.Fatalf("NormalizeBaseURL(%q) was accepted", input)
		}
	}
}

func TestNormalizeBaseURLDistinguishesRequiredFromInvalid(t *testing.T) {
	if _, err := NormalizeBaseURL(""); CodeOf(err) != CODE_BASE_URL_REQUIRED {
		t.Fatalf("empty: code = %q", CodeOf(err))
	}
	if _, err := NormalizeBaseURL("ftp://example.com"); CodeOf(err) != CODE_BASE_URL_INVALID {
		t.Fatalf("ftp: code = %q", CodeOf(err))
	}
	if got := ExitCode(Errorf(CODE_BASE_URL_REQUIRED, "")); got != EXIT_CONFIG {
		t.Fatalf("exit code = %d, want %d", got, EXIT_CONFIG)
	}
}

func TestIsInsecureBaseURL(t *testing.T) {
	cases := map[string]bool{
		"http://127.0.0.1:8317":     false,
		"http://localhost:8317":     false,
		"http://[::1]:8317":         false,
		"https://omc.example.com":   false,
		"http://cli-proxy-api:8317": true,
		"http://10.0.0.5:8317":      true,
	}
	for input, want := range cases {
		if got := IsInsecureBaseURL(input); got != want {
			t.Fatalf("IsInsecureBaseURL(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestResolveBaseURLPrecedence(t *testing.T) {
	config := Config{Version: CONFIG_VERSION, BaseURL: "http://from-config:8317"}

	flagResult, err := ResolveBaseURL("http://from-flag:8317", "http://from-env:8317", config)
	if err != nil {
		t.Fatal(err)
	}
	if flagResult.Root != "http://from-flag:8317" || flagResult.Source != "flag" {
		t.Fatalf("flag precedence lost: %+v", flagResult)
	}

	envResult, err := ResolveBaseURL("", "http://from-env:8317", config)
	if err != nil {
		t.Fatal(err)
	}
	if envResult.Root != "http://from-env:8317" || envResult.Source != "env" {
		t.Fatalf("env precedence lost: %+v", envResult)
	}

	configResult, err := ResolveBaseURL("", "", config)
	if err != nil {
		t.Fatal(err)
	}
	if configResult.Root != "http://from-config:8317" || configResult.Source != "config" {
		t.Fatalf("config precedence lost: %+v", configResult)
	}

	_, err = ResolveBaseURL("", "", Config{})
	if CodeOf(err) != CODE_BASE_URL_REQUIRED {
		t.Fatalf("no source: code = %q, want %q", CodeOf(err), CODE_BASE_URL_REQUIRED)
	}
}

func TestErrorExitCodes(t *testing.T) {
	cases := map[string]int{
		CODE_HOME_UNAVAILABLE:  EXIT_FAILED,
		CODE_KEY_NOT_FOUND:     EXIT_NOT_FOUND,
		CODE_ENVELOPE_MISSING:  EXIT_NOT_FOUND,
		CODE_BASE_URL_REQUIRED: EXIT_CONFIG,
		CODE_BASE_URL_INVALID:  EXIT_CONFIG,
		CODE_RUNTIME_UNKNOWN:   EXIT_USAGE,
		CODE_NOT_OWNED:         EXIT_FAILED,
		CODE_CONFIG_CONFLICT:   EXIT_CONFLICT,
		CODE_STATE_CORRUPT:     EXIT_CONFLICT,
	}
	for code, want := range cases {
		if got := ExitCode(Errorf(code, "detail")); got != want {
			t.Fatalf("ExitCode(%s) = %d, want %d", code, got, want)
		}
	}
	if got := ExitCode(nil); got != EXIT_OK {
		t.Fatalf("ExitCode(nil) = %d, want %d", got, EXIT_OK)
	}
	if got := ExitCode(os.ErrNotExist); got != EXIT_FAILED {
		t.Fatalf("an uncoded error should be a failure, got %d", got)
	}
}

func TestErrorDoesNotPrintItsCause(t *testing.T) {
	cause := os.ErrPermission
	err := Wrapf(cause, CODE_KEY_UNREADABLE, "cannot read the key")
	if text := err.Error(); strings.Contains(text, cause.Error()) {
		t.Fatalf("the coded message leaked its cause: %q", text)
	}
	if !strings.HasPrefix(err.Error(), CODE_KEY_UNREADABLE+": ") {
		t.Fatalf("the code should lead the message: %q", err.Error())
	}
}

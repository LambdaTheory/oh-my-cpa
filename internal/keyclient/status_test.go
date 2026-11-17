package keyclient

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// statusFixture builds the three cases together: a repository with a key and no
// envelope, one with both, and a runtime slot. They are the states a person has to tell
// apart when `key exec` refuses to start.
func statusFixture(t *testing.T) (Home, Report) {
	t.Helper()
	home := newTestHome(t)
	at := time.Date(2026, 11, 17, 9, 12, 0, 0, time.UTC)
	if _, err := Generate(GenOptions{Home: home, Repo: "org/full", Now: at}); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(GenOptions{Home: home, Repo: "org/key-only", Now: at}); err != nil {
		t.Fatal(err)
	}
	complete, _ := ciphertextResult(t, home, "org/full", "app-full", "sk-cpa-full")
	if _, err := Accept(AcceptOptions{Home: home, Repo: "org/full", Input: mustJSON(t, complete), Now: at}); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStoreForUpdate(home, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutRuntime(RuntimeCodex, RuntimeSlot{
		Repo: "org/full", InstalledAt: at, ConfigPath: "/home/user/.codex/config.toml", BackupPath: "/home/user/.omc/backups/b.toml",
	}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.Save(); err != nil {
		store.Close()
		t.Fatal(err)
	}
	store.Close()
	report, err := BuildReport(home, BaseURL{Root: "http://127.0.0.1:8317", Source: "config"}, "")
	if err != nil {
		t.Fatal(err)
	}
	return home, report
}

func reportFor(t *testing.T, report Report, repoID string) RepoReport {
	t.Helper()
	for _, candidate := range report.Repos {
		if candidate.Repo == repoID {
			return candidate
		}
	}
	t.Fatalf("the report has no %s row: %+v", repoID, report.Repos)
	return RepoReport{}
}

func TestBuildReportTellsTheThreeStatesApart(t *testing.T) {
	_, report := statusFixture(t)
	if len(report.Repos) != 2 {
		t.Fatalf("repos = %d, want 2", len(report.Repos))
	}
	if report.BaseURL != "http://127.0.0.1:8317" || report.BaseURLSource != "config" {
		t.Fatalf("gateway = %q (%q)", report.BaseURL, report.BaseURLSource)
	}
	complete := reportFor(t, report, "org/full")
	if !complete.PrivateKeyPresent || !complete.EnvelopePresent {
		t.Fatalf("org/full is missing its files: %+v", complete)
	}
	if complete.ApprovalStatus != APPROVAL_APPROVED || complete.StatusSource != STATUS_SOURCE_ACCEPT {
		t.Fatalf("org/full approval = %q/%q", complete.ApprovalStatus, complete.StatusSource)
	}
	if len(complete.Installed) != 1 || complete.Installed[0] != RuntimeCodex {
		t.Fatalf("org/full installed = %v", complete.Installed)
	}
	// The row must expose the file names and the mask, never a key value.
	if complete.KeyMask != "sk-cpa-ab***wxyz" || complete.PrivateKeyFile == "" || complete.EnvelopeFile == "" {
		t.Fatalf("org/full row = %+v", complete)
	}

	keyOnly := reportFor(t, report, "org/key-only")
	if !keyOnly.PrivateKeyPresent {
		t.Fatal("org/key-only should have its private key")
	}
	if keyOnly.EnvelopePresent {
		t.Fatal("org/key-only has no envelope, and the report says it does")
	}
	if keyOnly.ApprovalStatus != APPROVAL_NOT_APPLIED {
		t.Fatalf("org/key-only approval = %q", keyOnly.ApprovalStatus)
	}
	if len(keyOnly.Installed) != 0 {
		t.Fatalf("org/key-only installed = %v", keyOnly.Installed)
	}
	if len(report.Runtimes) != 1 || report.Runtimes[0].Runtime != RuntimeCodex {
		t.Fatalf("runtime slots = %+v", report.Runtimes)
	}
}

func TestBuildReportOnAMachineWithNoKeys(t *testing.T) {
	home := Home{Root: t.TempDir() + "/absent-home"}
	report, err := BuildReport(home, BaseURL{Source: "absent"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Repos) != 0 || report.OrphanKeys != 0 || report.OrphanEnvelopes != 0 {
		t.Fatalf("report = %+v", report)
	}
	var output bytes.Buffer
	WriteReport(&output, report)
	if !strings.Contains(output.String(), "holds no keys yet") || !strings.Contains(output.String(), "key gen") {
		t.Fatalf("the empty report does not name the next command:\n%s", output.String())
	}
	if !strings.Contains(output.String(), "not set") {
		t.Fatalf("the empty report should say the gateway is not set:\n%s", output.String())
	}
}

func TestBuildReportCountsOrphanFiles(t *testing.T) {
	home, _ := statusFixture(t)
	orphan := []byte("not a key this state knows")
	if err := os.WriteFile(home.KeysDir()+"/stranger-00000000.key", orphan, FILE_MODE); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(home.EnvelopesDir()+"/stranger-00000000.json", orphan, FILE_MODE); err != nil {
		t.Fatal(err)
	}
	report, err := BuildReport(home, BaseURL{Source: "absent"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if report.OrphanKeys != 1 || report.OrphanEnvelopes != 1 {
		t.Fatalf("orphans = %d/%d, want 1/1", report.OrphanKeys, report.OrphanEnvelopes)
	}
	for _, row := range report.Repos {
		if row.Repo == "stranger" {
			t.Fatal("an orphan file must not be shown as a bound repository")
		}
	}
}

func TestWriteReportJSONMatchesTheDocumentedShape(t *testing.T) {
	_, report := statusFixture(t)
	var output bytes.Buffer
	if err := WriteReportJSON(&output, report); err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
		t.Fatalf("status --json is not JSON: %v\n%s", err, output.String())
	}
	for _, field := range []string{"home", "base_url", "base_url_source", "repos", "runtimes"} {
		if _, ok := decoded[field]; !ok {
			t.Fatalf("status --json has no %q field:\n%s", field, output.String())
		}
	}
	repos, ok := decoded["repos"].([]any)
	if !ok || len(repos) != 2 {
		t.Fatalf("repos = %v", decoded["repos"])
	}
	found := false
	for _, entry := range repos {
		row, ok := entry.(map[string]any)
		if !ok {
			t.Fatalf("a repo entry is not an object: %v", entry)
		}
		for _, field := range []string{
			"repo", "repo_display", "private_key_file", "private_key_present",
			"envelope_file", "envelope_present", "installed",
		} {
			if _, ok := row[field]; !ok {
				t.Fatalf("a repo entry has no %q field: %v", field, row)
			}
		}
		if row["repo"] == "org/full" {
			found = true
			installed, ok := row["installed"].(map[string]any)
			if !ok || installed["codex"] != true {
				t.Fatalf("org/full installed = %v", row["installed"])
			}
			if row["key_mask"] != "sk-cpa-ab***wxyz" {
				t.Fatalf("org/full key mask = %v", row["key_mask"])
			}
		}
	}
	if !found {
		t.Fatalf("status --json has no org/full row:\n%s", output.String())
	}
	// The report may name the envelope file, but it must not carry an envelope body or a
	// plaintext key; the mask is the only key material a person reads.
	for _, forbidden := range []string{"sk-cpa-plaintext", "sk-cpa-full"} {
		if strings.Contains(output.String(), forbidden) {
			t.Fatalf("status --json carries %q:\n%s", forbidden, output.String())
		}
	}
}

func TestWriteReportNamesAMissingFileAndItsCommand(t *testing.T) {
	_, report := statusFixture(t)
	var output bytes.Buffer
	WriteReport(&output, report)
	text := output.String()
	if !strings.Contains(text, "envelope:    missing") || !strings.Contains(text, "key accept --repo org/key-only") {
		t.Fatalf("the key-only row does not tell the reader to accept an envelope:\n%s", text)
	}
	if !strings.Contains(text, "org/full") || !strings.Contains(text, "installed:   codex") {
		t.Fatalf("the complete row does not name its runtime:\n%s", text)
	}
}

func TestAssociateApplicationRecordsAnIdWithoutAnEnvelope(t *testing.T) {
	home := newTestHome(t)
	if _, err := Generate(GenOptions{Home: home, Repo: "org/repo", Now: time.Now()}); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	record, err := AssociateApplication(home, "org/repo", "app-1", at)
	if err != nil {
		t.Fatal(err)
	}
	if record.ApplicationID != "app-1" || record.ApprovalStatus != APPROVAL_UNKNOWN || record.StatusSource != STATUS_SOURCE_LOCAL {
		t.Fatalf("record = %+v", record)
	}
	if !record.StatusUpdatedAt.Equal(at) {
		t.Fatalf("status updated at = %v", record.StatusUpdatedAt)
	}
	if _, err := AssociateApplication(home, "org/absent", "app-2", at); CodeOf(err) != CODE_KEY_NOT_FOUND {
		t.Fatalf("an unknown repository should be key_not_found, got %v", err)
	}
}

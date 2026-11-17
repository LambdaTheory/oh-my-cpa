package keyclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// RepoReport is one repository in `key status` output: what this machine holds for it,
// and what it last learned about its application. It carries file names, a public key, a
// mask and fingerprints; never a private key, a plaintext gateway key or an envelope body.
type RepoReport struct {
	Repo               string
	RepoDisplay        string
	PublicKey          string
	Fingerprint        string
	PrivateKeyFile     string
	PrivateKeyPresent  bool
	EnvelopeFile       string
	EnvelopePresent    bool
	EnvelopeReceivedAt time.Time
	ApplicationID      string
	ApplicationKind    string
	ApprovalStatus     string
	StatusSource       string
	StatusUpdatedAt    time.Time
	DecidedAt          time.Time
	KeyMask            string
	RepoState          string
	RepoStateUpdatedAt time.Time
	Installed          []Runtime
	// RemoteError names why the last `--remote` read of this repository failed. When it is
	// set, the approval and repository state above are the last local values, not fresh
	// ones, and the row says so instead of presenting them as current.
	RemoteError string
}

// RuntimeReport is one runtime's installed slot. It names no value: the fingerprint in
// state is enough to tell whether the slot still holds this machine's key.
type RuntimeReport struct {
	Runtime     Runtime
	Repo        string
	InstalledAt time.Time
	ConfigPath  string
	BackupPath  string
}

// Report is the whole local view. Orphan files are counted, not adopted: a key file whose
// state record is gone cannot be proven to belong to any repository, so it is reported so
// a person can decide, rather than shown as a bound key.
type Report struct {
	Home            Home
	BaseURL         string
	BaseURLSource   string
	Repos           []RepoReport
	Runtimes        []RuntimeReport
	OrphanKeys      int
	OrphanEnvelopes int
}

// BuildReport reads $OMC_HOME and produces the local view.
//
// It never creates anything: a machine that has never run `key gen` reports an empty list
// and the next command to run, because status is the first command a person reaches for.
func BuildReport(home Home, baseURL BaseURL, filterRepo string) (Report, error) {
	report := Report{Home: home, BaseURL: baseURL.Root, BaseURLSource: baseURL.Source}
	if report.BaseURLSource == "" {
		report.BaseURLSource = "absent"
	}
	if info, err := os.Stat(home.Root); err != nil || !info.IsDir() {
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return Report{}, Wrapf(err, CODE_HOME_UNAVAILABLE, "cannot read %s: %v", home.Root, err)
		}
		if filterRepo != "" {
			return Report{}, Errorf(CODE_KEY_NOT_FOUND, "no key for %s; run `oh-my-cpa key gen --repo %s`", filterRepo, filterRepo)
		}
		return report, nil
	}
	store, err := OpenStore(home)
	if err != nil {
		return Report{}, err
	}
	state := store.State()
	slots := map[string][]Runtime{}
	for _, runtime := range Runtimes() {
		slot, ok := state.Runtimes[runtime]
		if !ok || strings.TrimSpace(slot.Repo) == "" {
			continue
		}
		slots[slot.Repo] = append(slots[slot.Repo], runtime)
		report.Runtimes = append(report.Runtimes, RuntimeReport{
			Runtime: runtime, Repo: slot.Repo, InstalledAt: slot.InstalledAt,
			ConfigPath: slot.ConfigPath, BackupPath: slot.BackupPath,
		})
	}
	repoIDs := make([]string, 0, len(state.Repos))
	for repoID := range state.Repos {
		repoIDs = append(repoIDs, repoID)
	}
	sort.Strings(repoIDs)
	for _, repoID := range repoIDs {
		if filterRepo != "" && filterRepo != repoID {
			continue
		}
		record := state.Repos[repoID]
		repoReport, err := buildRepoReport(home, record)
		if err != nil {
			return Report{}, err
		}
		repoReport.Installed = slots[repoID]
		report.Repos = append(report.Repos, repoReport)
	}
	if filterRepo != "" && len(report.Repos) == 0 {
		return Report{}, Errorf(CODE_KEY_NOT_FOUND, "no key for %s; run `oh-my-cpa key gen --repo %s`", filterRepo, filterRepo)
	}
	report.OrphanKeys, report.OrphanEnvelopes, err = countOrphanFiles(home, state)
	if err != nil {
		return Report{}, err
	}
	return report, nil
}

// buildRepoReport fills one repository's row, checking file presence without reading a
// key: status must not need the private key, and a present-but-damaged file is a later
// command's failure to report, not this one's.
func buildRepoReport(home Home, record RepoState) (RepoReport, error) {
	report := RepoReport{
		Repo:               record.Repo,
		RepoDisplay:        record.RepoDisplay,
		PublicKey:          record.PublicKey,
		Fingerprint:        record.Fingerprint,
		PrivateKeyFile:     record.PrivateKeyFile,
		EnvelopeFile:       record.EnvelopeFile,
		EnvelopeReceivedAt: record.EnvelopeReceivedAt,
		ApplicationID:      record.ApplicationID,
		ApplicationKind:    record.ApplicationKind,
		ApprovalStatus:     record.ApprovalStatus,
		StatusSource:       record.StatusSource,
		StatusUpdatedAt:    record.StatusUpdatedAt,
		DecidedAt:          record.DecidedAt,
		KeyMask:            record.KeyMask,
		RepoState:          record.RepoState,
		RepoStateUpdatedAt: record.RepoStateUpdatedAt,
	}
	keyRelative := record.PrivateKeyFile
	if keyRelative == "" {
		relative, err := home.KeyRelPath(record.Repo)
		if err != nil {
			return RepoReport{}, err
		}
		keyRelative = relative
	}
	report.PrivateKeyFile = keyRelative
	report.PrivateKeyPresent = isRegularFile(filepath.Join(home.Root, filepath.FromSlash(keyRelative)))
	envelopeRelative := record.EnvelopeFile
	if envelopeRelative == "" {
		relative, err := home.EnvelopeRelPath(record.Repo)
		if err != nil {
			return RepoReport{}, err
		}
		envelopeRelative = relative
	}
	report.EnvelopeFile = envelopeRelative
	report.EnvelopePresent = isRegularFile(filepath.Join(home.Root, filepath.FromSlash(envelopeRelative)))
	return report, nil
}

// isRegularFile reports whether path is a regular file, treating any stat failure as
// absent. Status is a read-only report; a directory it cannot inspect is shown as missing
// rather than turned into an error that hides the rest of the list.
func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// countOrphanFiles counts key and envelope files that no state record names.
func countOrphanFiles(home Home, state State) (keys int, envelopes int, err error) {
	knownKeys := map[string]bool{}
	knownEnvelopes := map[string]bool{}
	for _, record := range state.Repos {
		if record.PrivateKeyFile != "" {
			knownKeys[filepath.Clean(filepath.FromSlash(record.PrivateKeyFile))] = true
		}
		if record.EnvelopeFile != "" {
			knownEnvelopes[filepath.Clean(filepath.FromSlash(record.EnvelopeFile))] = true
		}
	}
	countKeys, err := countUnnamed(home.KeysDir(), knownKeys)
	if err != nil {
		return 0, 0, err
	}
	countEnvelopes, err := countUnnamed(home.EnvelopesDir(), knownEnvelopes)
	if err != nil {
		return 0, 0, err
	}
	return countKeys, countEnvelopes, nil
}

// countUnnamed counts the entries of dir that are not in known. A missing directory has
// no files, which is the normal state of a home that has only ever held a private key.
func countUnnamed(dir string, known map[string]bool) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, Wrapf(err, CODE_HOME_UNAVAILABLE, "cannot read %s: %v", dir, err)
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if !known[filepath.Clean(filepath.Join(filepath.Base(dir), entry.Name()))] {
			count++
		}
	}
	return count, nil
}

// RepoLabelOf names one report row the way messages name a repository: the submitted
// spelling, with the normalized identity beside it when they differ.
func RepoLabelOf(report RepoReport) string {
	display := strings.TrimSpace(report.RepoDisplay)
	if display == "" || display == report.Repo {
		return report.Repo
	}
	return fmt.Sprintf("%s (%s)", display, report.Repo)
}

// WriteReport renders the human view: a header, a block per repository and a block per
// installed runtime. It is what a person reads before running `key exec`, so it says what
// is missing as plainly as what is present.
func WriteReport(writer io.Writer, report Report) {
	fmt.Fprintf(writer, "home: %s\n", report.Home.Root)
	if report.BaseURL == "" {
		fmt.Fprintln(writer, "gateway: not set; pass --base-url or set "+ENV_CPA_BASE_URL)
	} else {
		fmt.Fprintf(writer, "gateway: %s (%s)\n", report.BaseURL, report.BaseURLSource)
	}
	if len(report.Repos) == 0 {
		fmt.Fprintln(writer, "\nthis machine holds no keys yet. Next: `oh-my-cpa key gen --repo <org/repo>`, then apply for the key and store it with `oh-my-cpa key accept --repo <org/repo>`")
	} else {
		for _, repoReport := range report.Repos {
			fmt.Fprintf(writer, "\n%s\n", RepoLabelOf(repoReport))
			if repoReport.PrivateKeyPresent {
				fmt.Fprintf(writer, "  private key: %s\n", repoReport.PrivateKeyFile)
			} else {
				fmt.Fprintf(writer, "  private key: missing (%s); run `oh-my-cpa key gen --repo %s`\n", repoReport.PrivateKeyFile, repoReport.Repo)
			}
			switch {
			case repoReport.EnvelopePresent:
				fmt.Fprintf(writer, "  envelope:    %s, received %s\n", repoReport.EnvelopeFile, formatTime(repoReport.EnvelopeReceivedAt))
			case repoReport.PrivateKeyPresent:
				fmt.Fprintf(writer, "  envelope:    missing; run `oh-my-cpa key accept --repo %s` with the repo_key_ciphertext result on stdin\n", repoReport.Repo)
			default:
				fmt.Fprintln(writer, "  envelope:    missing")
			}
			if repoReport.ApplicationID != "" {
				kind := repoReport.ApplicationKind
				if kind == "" {
					kind = "unspecified"
				}
				fmt.Fprintf(writer, "  application: %s (%s)\n", repoReport.ApplicationID, kind)
			}
			fmt.Fprintf(writer, "  approval:    %s\n", describeApproval(repoReport))
			if repoReport.KeyMask != "" {
				fmt.Fprintf(writer, "  key mask:    %s\n", repoReport.KeyMask)
			}
			if repoReport.RepoState != "" {
				fmt.Fprintf(writer, "  repo state:  %s (as of %s)\n", repoReport.RepoState, formatTime(repoReport.RepoStateUpdatedAt))
			}
			if repoReport.RemoteError != "" {
				fmt.Fprintf(writer, "  remote:      %s; the values above are the last local state\n", repoReport.RemoteError)
			}
			if len(repoReport.Installed) == 0 {
				fmt.Fprintln(writer, "  installed:   no runtime holds this key")
			} else {
				for _, runtime := range repoReport.Installed {
					fmt.Fprintf(writer, "  installed:   %s\n", runtime)
				}
			}
		}
	}
	if len(report.Runtimes) > 0 {
		fmt.Fprintln(writer, "\nruntime slots:")
		for _, slot := range report.Runtimes {
			fmt.Fprintf(writer, "  %-8s %s, installed %s\n", slot.Runtime, slot.Repo, formatTime(slot.InstalledAt))
			if slot.ConfigPath != "" {
				fmt.Fprintf(writer, "           config %s\n", slot.ConfigPath)
			}
			if slot.BackupPath != "" {
				fmt.Fprintf(writer, "           backup %s\n", slot.BackupPath)
			}
		}
	}
	if report.OrphanKeys > 0 || report.OrphanEnvelopes > 0 {
		fmt.Fprintf(writer, "\n%d key file(s) and %d envelope(s) are not named by %s; rebuild their records with `oh-my-cpa key gen --repo <repo>` and `oh-my-cpa key accept --repo <repo>`\n",
			report.OrphanKeys, report.OrphanEnvelopes, report.Home.StatePath())
	}
}

// describeApproval renders the approval line with where and when it was learned.
func describeApproval(report RepoReport) string {
	status := report.ApprovalStatus
	if status == "" {
		status = "unknown"
	}
	switch {
	case report.StatusSource != "" && !report.StatusUpdatedAt.IsZero():
		return fmt.Sprintf("%s (%s, %s)", status, report.StatusSource, formatTime(report.StatusUpdatedAt))
	case report.StatusSource != "":
		return fmt.Sprintf("%s (%s)", status, report.StatusSource)
	default:
		return status
	}
}

// formatTime renders a state timestamp in the same shape state.json uses.
func formatTime(value time.Time) string {
	if value.IsZero() {
		return "never"
	}
	return value.UTC().Format(time.RFC3339)
}

// statusJSON is the stable `key status --json` shape. Field names are a contract a script
// reads, so they follow state.json's names and change only deliberately.
type statusJSON struct {
	Home          string           `json:"home"`
	BaseURL       string           `json:"base_url"`
	BaseURLSource string           `json:"base_url_source"`
	Repos         []repoStatusJSON `json:"repos"`
	Runtimes      map[string]any   `json:"runtimes"`
	Orphans       orphanStatusJSON `json:"orphan_files"`
}

type repoStatusJSON struct {
	Repo               string          `json:"repo"`
	RepoDisplay        string          `json:"repo_display"`
	PublicKey          string          `json:"public_key,omitempty"`
	Fingerprint        string          `json:"public_key_fingerprint,omitempty"`
	PrivateKeyFile     string          `json:"private_key_file"`
	PrivateKeyPresent  bool            `json:"private_key_present"`
	EnvelopeFile       string          `json:"envelope_file"`
	EnvelopePresent    bool            `json:"envelope_present"`
	EnvelopeReceivedAt string          `json:"envelope_received_at,omitempty"`
	ApplicationID      string          `json:"application_id,omitempty"`
	ApplicationKind    string          `json:"application_kind,omitempty"`
	ApprovalStatus     string          `json:"approval_status,omitempty"`
	StatusSource       string          `json:"status_source,omitempty"`
	StatusUpdatedAt    string          `json:"status_updated_at,omitempty"`
	DecidedAt          string          `json:"decided_at,omitempty"`
	KeyMask            string          `json:"key_mask,omitempty"`
	RepoState          string          `json:"repo_state,omitempty"`
	RepoStateUpdatedAt string          `json:"repo_state_updated_at,omitempty"`
	RemoteError        string          `json:"remote_error,omitempty"`
	Installed          map[string]bool `json:"installed"`
}

type runtimeStatusJSON struct {
	Repo        string `json:"repo"`
	InstalledAt string `json:"installed_at,omitempty"`
	ConfigPath  string `json:"config_path,omitempty"`
	BackupPath  string `json:"backup_path,omitempty"`
}

type orphanStatusJSON struct {
	Keys      int `json:"keys"`
	Envelopes int `json:"envelopes"`
}

// WriteReportJSON renders the machine view.
func WriteReportJSON(writer io.Writer, report Report) error {
	document := statusJSON{
		Home:          report.Home.Root,
		BaseURL:       report.BaseURL,
		BaseURLSource: report.BaseURLSource,
		Repos:         make([]repoStatusJSON, 0, len(report.Repos)),
		Runtimes:      map[string]any{},
		Orphans:       orphanStatusJSON{Keys: report.OrphanKeys, Envelopes: report.OrphanEnvelopes},
	}
	for _, repoReport := range report.Repos {
		installed := map[string]bool{}
		for _, runtime := range repoReport.Installed {
			installed[string(runtime)] = true
		}
		document.Repos = append(document.Repos, repoStatusJSON{
			Repo:               repoReport.Repo,
			RepoDisplay:        repoReport.RepoDisplay,
			PublicKey:          repoReport.PublicKey,
			Fingerprint:        repoReport.Fingerprint,
			PrivateKeyFile:     repoReport.PrivateKeyFile,
			PrivateKeyPresent:  repoReport.PrivateKeyPresent,
			EnvelopeFile:       repoReport.EnvelopeFile,
			EnvelopePresent:    repoReport.EnvelopePresent,
			EnvelopeReceivedAt: jsonTime(repoReport.EnvelopeReceivedAt),
			ApplicationID:      repoReport.ApplicationID,
			ApplicationKind:    repoReport.ApplicationKind,
			ApprovalStatus:     repoReport.ApprovalStatus,
			StatusSource:       repoReport.StatusSource,
			StatusUpdatedAt:    jsonTime(repoReport.StatusUpdatedAt),
			DecidedAt:          jsonTime(repoReport.DecidedAt),
			KeyMask:            repoReport.KeyMask,
			RepoState:          repoReport.RepoState,
			RepoStateUpdatedAt: jsonTime(repoReport.RepoStateUpdatedAt),
			RemoteError:        repoReport.RemoteError,
			Installed:          installed,
		})
	}
	for _, slot := range report.Runtimes {
		document.Runtimes[string(slot.Runtime)] = runtimeStatusJSON{
			Repo: slot.Repo, InstalledAt: jsonTime(slot.InstalledAt),
			ConfigPath: slot.ConfigPath, BackupPath: slot.BackupPath,
		}
	}
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return Wrapf(err, CODE_STATE_CORRUPT, "cannot encode the status report: %v", err)
	}
	_, err = writer.Write(append(encoded, '\n'))
	return err
}

// jsonTime renders a timestamp for the JSON report, leaving an unset one as an empty
// string rather than the zero time, which would read as year one.
func jsonTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

// ApplyRemoteFailures marks the rows whose last remote read failed, so a report built after
// a partial refresh does not present a stale local value as the current one.
func (r *Report) ApplyRemoteFailures(failures []RepoRefreshError) {
	for index := range r.Repos {
		for _, failure := range failures {
			if r.Repos[index].Repo == failure.Repo {
				r.Repos[index].RemoteError = "remote_error:" + failure.Code
			}
		}
	}
}

// AssociateApplication records that an application id belongs to a repository, which is
// the local half of `key status --repo <repo> --application-id <id>`: between apply and
// accept, the id is the only thing this machine knows about the application.
func AssociateApplication(home Home, repoID, applicationID string, now time.Time) (RepoState, error) {
	store, err := OpenStoreForUpdate(home, 0)
	if err != nil {
		return RepoState{}, err
	}
	defer store.Close()
	record, exists := store.Repo(repoID)
	if !exists {
		return RepoState{}, Errorf(CODE_KEY_NOT_FOUND, "no key for %s; run `oh-my-cpa key gen --repo %s` first", repoID, repoID)
	}
	record.ApplicationID = applicationID
	record.ApprovalStatus = APPROVAL_UNKNOWN
	record.StatusSource = STATUS_SOURCE_LOCAL
	record.StatusUpdatedAt = now.UTC()
	if err := store.PutRepo(record); err != nil {
		return RepoState{}, err
	}
	if err := store.Save(); err != nil {
		return RepoState{}, err
	}
	return record, nil
}

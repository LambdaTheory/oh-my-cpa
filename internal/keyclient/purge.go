package keyclient

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// PurgeOptions is one `key purge` run.
type PurgeOptions struct {
	// Repo is the repository identity to clean up.
	Repo string
	// Runtime limits the run to one runtime's installed value. The private key, the
	// envelope and the record stay, because the repository is still installed, or may
	// still be installed, somewhere else.
	Runtime Runtime
	DryRun  bool
	Home    Home
	Now     time.Time
	// Warn receives the notes that are not failures: a backup kept for someone else, a
	// value changed by something else.
	Warn io.Writer
}

// PurgeResult lists what a run removed, kept, and what still stands in the way.
type PurgeResult struct {
	// Removed is what was deleted; on a dry run, what would be.
	Removed []string
	// Kept is what the run deliberately left behind, with the reason.
	Kept []string
	// Pending is what belongs to this repository and is still installed, so a second run
	// can finish the job.
	Pending []string
	// BackupScanUnavailable is set when the private key is already gone: backups can
	// then only be recognised by what state recorded, never by their content.
	BackupScanUnavailable bool
}

// Purge removes a repository's local material — its installed values, the backups that
// hold its plaintext, its private key and envelope, and last its state record — in that
// order.
//
// The order is what makes it safe to repeat. Nothing is forgotten until everything else
// is gone, so an interrupted run can be run again and will finish; and the private key
// outlives the values it proves, because its plaintext is how purge decides that a value
// in a shared file really is this repository's.
//
// Purge is local. The key in the gateway stays valid: to revoke it, stop or reissue it
// in the console.
func Purge(options PurgeOptions) (PurgeResult, error) {
	// The identity is normalized, not taken verbatim, so a path or an SSH URL names the
	// record the install wrote and a hostile value never becomes a path component.
	repoID, _, err := DeriveRepoID(strings.TrimSpace(options.Repo))
	if err != nil {
		return PurgeResult{}, err
	}
	if options.Runtime != "" {
		if _, err := ParseRuntime(string(options.Runtime)); err != nil {
			return PurgeResult{}, err
		}
	}
	home := options.Home

	var store *Store
	if options.DryRun {
		// A dry run reads state the way a read-only command does: asking about a change
		// must not create a home directory.
		store, err = OpenStore(home)
	} else {
		store, err = OpenStoreForUpdate(home, 0)
	}
	if err != nil {
		return PurgeResult{}, err
	}
	defer store.Close()

	result := PurgeResult{}
	state := store.State()
	_, hasRecord := store.Repo(repoID)
	// remaining is the slot set after this run, whether or not it is a dry run: the
	// record decision has to be the same in both, or a dry run would always report
	// pending and read as if the purge could not finish.
	remaining := make(map[Runtime]RuntimeSlot, len(state.Runtimes))
	for runtime, slot := range state.Runtimes {
		remaining[runtime] = slot
	}

	// Step 2: open the envelope once if the private key is still here. The plaintext
	// stays in this call's memory only, and is what lets the later steps recognise this
	// repository's values and backups instead of guessing from a filename.
	plaintext, err := purgeKeyMaterial(home, repoID, &result)
	if err != nil {
		return PurgeResult{}, err
	}
	fingerprint := ""
	if plaintext != "" {
		fingerprint = KeyValueFingerprint(plaintext)
	}

	// Steps 3 and 4: every runtime this run covers.
	runtimes := Runtimes()
	if options.Runtime != "" {
		runtimes = []Runtime{options.Runtime}
	}
	for _, runtime := range runtimes {
		slot, hasSlot := state.Runtimes[runtime]
		if hasSlot && slot.Repo != repoID {
			result.Kept = append(result.Kept, fmt.Sprintf("%s: holds %s, not %s", runtime, slot.Repo, repoID))
			continue
		}
		if !hasSlot && options.Runtime != "" {
			result.Kept = append(result.Kept, fmt.Sprintf("%s: no install record for %s", runtime, repoID))
			continue
		}
		configPath := slot.ConfigPath
		if configPath == "" {
			located, pathErr := RuntimeConfigPath(runtime, "")
			if pathErr != nil {
				result.Kept = append(result.Kept, fmt.Sprintf("%s: cannot locate its configuration (%s)", runtime, MessageOf(pathErr)))
				continue
			}
			configPath = located
		}
		outcome, err := purgeRuntime(runtime, configPath, repoID, slot, plaintext, fingerprint, options)
		if err != nil {
			return PurgeResult{}, err
		}
		result.Removed = append(result.Removed, outcome.Removed...)
		result.Kept = append(result.Kept, outcome.Kept...)
		if outcome.Pending {
			result.Pending = append(result.Pending, runtimePurgeTargets(runtime, configPath)...)
			continue
		}
		if hasSlot {
			delete(remaining, runtime)
			if !options.DryRun {
				store.RemoveRuntime(runtime)
			}
		}
	}

	// Step 5: backups. One that holds this repository's plaintext goes, because keeping
	// it would leave the key on disk; every other backup is somebody's restore point and
	// stays. This is a full-run step: a single-runtime run leaves the material it does
	// not otherwise touch alone.
	if options.Runtime == "" {
		removedBackups, keptBackups, backupErr := purgeBackups(home, repoID, plaintext, state.Runtimes, options.DryRun)
		if backupErr != nil {
			return PurgeResult{}, backupErr
		}
		result.Removed = append(result.Removed, removedBackups...)
		result.Kept = append(result.Kept, keptBackups...)
	}

	// Step 6: the private key and the envelope.
	if options.Runtime == "" {
		keyPath, err := home.KeyPath(repoID)
		if err != nil {
			return PurgeResult{}, err
		}
		hasKey, err := HasPrivateKey(home, repoID)
		if err != nil {
			return PurgeResult{}, err
		}
		if hasKey {
			if err := removeFile(options.DryRun, keyPath); err != nil {
				result.Pending = append(result.Pending, keyPath)
			} else {
				result.Removed = append(result.Removed, keyPath)
			}
		}
		envelopePath, err := home.EnvelopePath(repoID)
		if err != nil {
			return PurgeResult{}, err
		}
		hasEnvelope, err := HasEnvelope(home, repoID)
		if err != nil {
			return PurgeResult{}, err
		}
		if hasEnvelope {
			if err := removeFile(options.DryRun, envelopePath); err != nil {
				result.Pending = append(result.Pending, envelopePath)
			} else {
				result.Removed = append(result.Removed, envelopePath)
			}
		}
	}

	// Step 7: the record, last.
	//
	// A single-runtime run leaves it: the key and envelope stay by design, and the record
	// is what points at them. A full run deletes it only once nothing is pending and no
	// runtime still holds this repository — that condition is also why an interrupted run
	// stays repeatable.
	switch {
	case hasRecord && options.Runtime != "":
		result.Kept = append(result.Kept, fmt.Sprintf("state record for %s (a --runtime run keeps the key and envelope)", repoID))
	case hasRecord && len(result.Pending) == 0 && allSlotsCleared(remaining, repoID):
		if !options.DryRun {
			store.RemoveRepo(repoID)
		}
		result.Removed = append(result.Removed, home.StatePath()+" (record for "+repoID+")")
	case hasRecord:
		result.Pending = append(result.Pending, "state record for "+repoID)
	}

	if !options.DryRun {
		if err := store.Save(); err != nil {
			return PurgeResult{}, err
		}
	}
	if len(result.Pending) > 0 {
		return result, Errorf(CODE_PURGE_PENDING, "some material for %s was not removed; fix what is listed and run purge again", repoID)
	}
	return result, nil
}

// purgeKeyMaterial opens the repository's envelope while the private key is still here.
//
// A key with no envelope is a half-finished setup: the key still has to go, so a missing
// envelope is not an error. With the key already gone, backups can only be recognised
// from the record.
func purgeKeyMaterial(home Home, repoID string, result *PurgeResult) (string, error) {
	hasKey, err := HasPrivateKey(home, repoID)
	if err != nil {
		return "", err
	}
	if !hasKey {
		result.BackupScanUnavailable = true
		return "", nil
	}
	plaintext, openErr := InstallKeyMaterial(home, repoID)
	switch {
	case openErr == nil:
		return plaintext, nil
	case CodeOf(openErr) == CODE_ENVELOPE_MISSING:
		return "", nil
	default:
		return "", openErr
	}
}

// purgeOutcome is one runtime's part of a purge.
type purgeOutcome struct {
	Removed []string
	Kept    []string
	Pending bool
}

// purgeRuntime removes this repository's value from one runtime.
//
// The value goes only when it is provably this repository's: the file holds the
// plaintext the envelope opened, or the fingerprint state recorded. A file that holds
// something else was changed after the install, and removing it would undo a choice the
// person made on purpose — so it is reported, not deleted.
func purgeRuntime(runtime Runtime, configPath, repoID string, slot RuntimeSlot, plaintext, fingerprint string, options PurgeOptions) (purgeOutcome, error) {
	outcome := purgeOutcome{}
	content, existed, err := readExistingConfig(configPath)
	if err != nil {
		if CodeOf(err) == CODE_CONFIG_CONFLICT {
			// A link: rewriting through it would change a file this tool cannot see the
			// end of, so the value stays and the reason is said.
			if _, statErr := os.Lstat(configPath); statErr == nil {
				outcome.Kept = append(outcome.Kept, fmt.Sprintf("%s: %s is a link, so it was not edited", runtime, configPath))
			}
			return outcome, nil
		}
		return outcome, err
	}
	if !existed || len(bytes.TrimSpace(content)) == 0 {
		return outcome, nil
	}
	current, err := runtimeCurrentValueForSlot(runtime, content)
	if err != nil {
		outcome.Kept = append(outcome.Kept, fmt.Sprintf("%s: %s could not be read (%s)", runtime, configPath, MessageOf(err)))
		return outcome, nil
	}
	if current == "" {
		return outcome, nil
	}
	// The proof of ownership is the plaintext this run opened, or — when the private key
	// is already gone — the fingerprint state recorded for this slot. Anything else means
	// the value was changed after the install.
	if fingerprint == "" && slot.Repo == repoID {
		fingerprint = slot.ValueFingerprint
	}
	matches := (plaintext != "" && current == plaintext) ||
		(fingerprint != "" && KeyValueFingerprint(current) == fingerprint)
	if !matches {
		return outcome, Errorf(CODE_NOT_OWNED, "%s no longer holds the key this tool installed for %s; it was changed by something else, so it is left alone — remove it by hand if that is what you want", configPath, repoID)
	}
	if options.DryRun {
		outcome.Removed = append(outcome.Removed, runtimePurgeTargets(runtime, configPath)...)
		return outcome, nil
	}
	edited, changed, editErr := removeRuntimeEdit(runtime, content, slot)
	if editErr != nil {
		return outcome, editErr
	}
	if !changed {
		return outcome, nil
	}
	if err := writeFileAtomic(configPath, edited); err != nil {
		return outcome, Wrapf(err, CODE_INSTALL_FAILED, "cannot write %s; nothing was removed from it", configPath)
	}
	after, err := os.ReadFile(configPath)
	if err != nil {
		return outcome, err
	}
	remaining, err := runtimeCurrentValueForSlot(runtime, after)
	if err != nil {
		return outcome, err
	}
	if remaining != "" {
		outcome.Pending = true
		return outcome, nil
	}
	outcome.Removed = append(outcome.Removed, runtimePurgeTargets(runtime, configPath)...)
	return outcome, nil
}

// runtimePurgeTargets names what a runtime removal took out, for the report.
func runtimePurgeTargets(runtime Runtime, configPath string) []string {
	switch runtime {
	case RuntimeClaude:
		return []string{configPath + " (env." + CLAUDE_ENV_BASE_URL + ", env." + CLAUDE_ENV_KEY + ")"}
	case RuntimeCodex:
		return []string{configPath + " ([model_providers." + CODEX_PROVIDER_ID + "] and its " + CODEX_MODEL_PROVIDER + " line)"}
	case RuntimeOpenCode:
		return []string{configPath + " (provider." + OPENCODE_PROVIDER_ID + ")"}
	default:
		return []string{configPath}
	}
}

// removeRuntimeEdit removes this tool's members and nothing else.
func removeRuntimeEdit(runtime Runtime, content []byte, slot RuntimeSlot) ([]byte, bool, error) {
	switch runtime {
	case RuntimeClaude:
		object, err := parseConfigObject(content)
		if err != nil {
			return nil, false, err
		}
		env, err := object.child("env")
		if err != nil {
			return nil, false, err
		}
		removed := env.delete(CLAUDE_ENV_KEY)
		if env.delete(CLAUDE_ENV_BASE_URL) {
			removed = true
		}
		if !removed {
			return content, false, nil
		}
		// An env object this tool emptied has no purpose left; one that holds anything
		// else keeps it, because that content is the operator's.
		if len(env.keys) == 0 {
			object.delete("env")
		} else if err := object.setChild("env", env); err != nil {
			return nil, false, err
		}
		encoded, err := object.encodeIndent()
		if err != nil {
			return nil, false, Wrapf(err, CODE_INSTALL_FAILED, "cannot encode the settings file: %v", err)
		}
		return encoded, true, nil
	case RuntimeOpenCode:
		object, err := parseConfigObject(content)
		if err != nil {
			return nil, false, err
		}
		provider, err := object.child("provider")
		if err != nil {
			return nil, false, err
		}
		if !provider.delete(OPENCODE_PROVIDER_ID) {
			return content, false, nil
		}
		if len(provider.keys) == 0 {
			object.delete("provider")
		} else if err := object.setChild("provider", provider); err != nil {
			return nil, false, err
		}
		encoded, err := object.encodeIndent()
		if err != nil {
			return nil, false, Wrapf(err, CODE_INSTALL_FAILED, "cannot encode the provider file: %v", err)
		}
		return encoded, true, nil
	case RuntimeCodex:
		return removeCodexEdit(content, slot)
	default:
		return nil, false, Errorf(CODE_RUNTIME_UNKNOWN, "unknown runtime %q", runtime)
	}
}

// removeCodexEdit deletes the managed block and hands the top-level model_provider back
// to whatever the install replaced.
//
// The two are handled independently because either can outlive the other: someone who
// deleted the block by hand leaves a model_provider line that points at a provider which
// no longer exists, and a config with no block at all would otherwise look clean while
// still naming this tool's provider.
func removeCodexEdit(content []byte, slot RuntimeSlot) ([]byte, bool, error) {
	document, err := parseConfig(content)
	if err != nil {
		return nil, false, err
	}
	changed := false
	start, end, found, err := document.blockMarkerPositions()
	if err != nil {
		return nil, false, Wrapf(err, CODE_CONFIG_CONFLICT, "the managed block is damaged: %v", err)
	}
	if found {
		document.removeBlock(start, end)
		changed = true
	}
	_, value, hasProvider, err := document.topLevelAssignment(CODEX_MODEL_PROVIDER)
	if err != nil {
		return nil, false, Wrapf(err, CODE_CONFIG_CONFLICT, "the top-level %s line cannot be read: %v", CODEX_MODEL_PROVIDER, err)
	}
	// Only a line this install set is restored. Somebody who chose another provider after
	// the install made a choice this tool will not undo behind their back.
	if hasProvider && value == CODEX_PROVIDER_ID {
		if slot.PreviousModelProvider != nil && strings.TrimSpace(*slot.PreviousModelProvider) != "" {
			if err := document.setTopLevelKey(CODEX_MODEL_PROVIDER, *slot.PreviousModelProvider); err != nil {
				return nil, false, Wrapf(err, CODE_CONFIG_CONFLICT, "cannot restore the previous %s: %v", CODEX_MODEL_PROVIDER, err)
			}
		} else if err := document.deleteTopLevelKey(CODEX_MODEL_PROVIDER); err != nil {
			return nil, false, Wrapf(err, CODE_CONFIG_CONFLICT, "cannot remove the %s line: %v", CODEX_MODEL_PROVIDER, err)
		}
		changed = true
	}
	if !changed {
		return content, false, nil
	}
	return document.encode(), true, nil
}

// purgeBackups removes the backups that hold this repository's plaintext key.
//
// The directory is shared by every install, so the decision is per file: a backup that
// contains the key cannot outlive it, and every other backup is the only way back from
// somebody else's install and stays. An unreadable file is kept and reported rather than
// deleted on a guess.
func purgeBackups(home Home, repoID, plaintext string, slots map[Runtime]RuntimeSlot, dryRun bool) (removed, kept []string, err error) {
	directory := home.BackupsDir()
	entries, readErr := os.ReadDir(directory)
	if readErr != nil {
		if errors.Is(readErr, os.ErrNotExist) {
			return nil, nil, nil
		}
		return nil, nil, Wrapf(readErr, CODE_STATE_UNREADABLE, "cannot read %s: %v", directory, readErr)
	}
	// The record is the only proof available without the plaintext: it says which backup
	// this install took, and whether that backup already held the key.
	recorded := map[string]bool{}
	for _, slot := range slots {
		if slot.Repo == repoID && slot.BackupPath != "" {
			recorded[filepath.Clean(slot.BackupPath)] = true
		}
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Join(directory, name)
		content, contentErr := os.ReadFile(path)
		if contentErr != nil {
			kept = append(kept, fmt.Sprintf("%s (cannot read it, so it was not removed)", path))
			continue
		}
		if plaintext != "" {
			if !bytes.Contains(content, []byte(plaintext)) {
				kept = append(kept, path)
				continue
			}
		} else if !recorded[filepath.Clean(path)] {
			kept = append(kept, fmt.Sprintf("%s (the private key is gone, so its content cannot be checked)", path))
			continue
		}
		if dryRun {
			removed = append(removed, path)
			continue
		}
		if removeErr := os.Remove(path); removeErr != nil {
			kept = append(kept, fmt.Sprintf("%s (cannot remove it: %v)", path, removeErr))
			continue
		}
		removed = append(removed, path)
	}
	return removed, kept, nil
}

// allSlotsCleared reports whether no runtime holds this repository any more.
func allSlotsCleared(slots map[Runtime]RuntimeSlot, repoID string) bool {
	for _, slot := range slots {
		if slot.Repo == repoID {
			return false
		}
	}
	return true
}

// removeFile deletes a path, tolerating one already gone so a repeated purge is not a
// failure.
func removeFile(dryRun bool, path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return Wrapf(err, CODE_KEY_UNREADABLE, "cannot inspect %s: %v", path, err)
	}
	if info.IsDir() {
		return Errorf(CODE_PURGE_PENDING, "%s is a directory, not a key file", path)
	}
	if dryRun {
		return nil
	}
	if err := os.Remove(path); err != nil {
		return Wrapf(err, CODE_PURGE_PENDING, "cannot remove %s: %v", path, err)
	}
	return nil
}

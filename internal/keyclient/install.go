package keyclient

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/repokey"
)

const (
	// OPENCODE_NPM is the package a custom OpenAI-compatible provider is declared
	// with. It is a declaration, not an install: OpenCode resolves it itself.
	OPENCODE_NPM = "@ai-sdk/openai-compatible"

	// OPENCODE_PROVIDER_ID and ENV_OPENCODE_CONFIG are declared in runtime.go, beside
	// the other OpenCode variables exec reads, so install and exec cannot disagree about
	// the provider name or the configuration path. ENV_CLAUDE_CONFIG_DIR and
	// ENV_CODEX_HOME are the same kind of variable for the other two runtimes: honouring
	// them is what keeps install writing the file the runtime will actually read.
	ENV_CLAUDE_CONFIG_DIR = "CLAUDE_CONFIG_DIR"
	ENV_CODEX_HOME        = "CODEX_HOME"
)

// The members install writes, by runtime. They are also what purge removes, and the
// ownership check reads the token member.
const (
	CLAUDE_ENV_KEY        = "ANTHROPIC_AUTH_TOKEN"
	CLAUDE_ENV_BASE_URL   = "ANTHROPIC_BASE_URL"
	CLAUDE_ENV_API_KEY    = "ANTHROPIC_API_KEY"
	CLAUDE_SETTINGS_FILE  = "settings.json"
	CLAUDE_CONFIG_DIRNAME = ".claude"
	CODEX_CONFIG_DIRNAME  = ".codex"
	CODEX_CONFIG_FILE     = "config.toml"
	OPENCODE_CONFIG_DIR   = ".config/opencode"
	OPENCODE_CONFIG_FILE  = "opencode.json"
	CODEX_MODEL_PROVIDER  = "model_provider"
)

// KeyValueFingerprint is the non-secret identity of a plaintext gateway key. It is
// what state records and what the ownership check compares, so a configuration that
// holds somebody else's value is never overwritten or deleted by mistake.
//
// A plain SHA-256 is enough: the key is high entropy, so the digest cannot be turned
// back into it, and unlike an HMAC it needs no second secret to verify.
func KeyValueFingerprint(value string) string {
	digest := sha256.Sum256([]byte(value))
	return "sha256:" + base64.RawURLEncoding.EncodeToString(digest[:])
}

// InstallKeyMaterial reads the repository's envelope and opens it with the local
// private key, returning the plaintext gateway key for one install.
//
// The plaintext exists only in the caller's memory. Nothing here writes it anywhere,
// and a repository that is missing either half is refused before a backup is made, so
// a failed install leaves no trace at all.
func InstallKeyMaterial(home Home, repoID string) (string, error) {
	if _, err := ReadPrivateKey(home, repoID); err != nil {
		return "", err
	}
	envelopePath, err := home.EnvelopePath(repoID)
	if err != nil {
		return "", err
	}
	envelope, err := os.ReadFile(envelopePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", Errorf(CODE_ENVELOPE_MISSING, "no delivery envelope for %s; run `oh-my-cpa key accept --repo %s` with the ciphertext from the console", repoID, repoID)
		}
		return "", Wrapf(err, CODE_ENVELOPE_INVALID, "cannot read %s: %v", envelopePath, err)
	}
	key, decryptErr := decryptEnvelope(home, repoID, envelope)
	if decryptErr != nil {
		return "", decryptErr
	}
	return key, nil
}

// decryptEnvelope opens envelope with the repository's private key. A failure here
// means the envelope was sealed to another public key, which is reported as
// decrypt_failed without quoting either half.
func decryptEnvelope(home Home, repoID string, envelope []byte) (string, error) {
	privateKey, err := ReadPrivateKey(home, repoID)
	if err != nil {
		return "", err
	}
	plaintext, err := repokey.Decrypt(privateKey, string(envelope))
	if err != nil {
		return "", Errorf(CODE_DECRYPT_FAILED, "the envelope for %s does not open with the local private key; ask the console for a new one", repoID)
	}
	return plaintext, nil
}

// RuntimeConfigPath is the file a runtime reads its settings from: the explicit
// --config value, then the runtime's own relocation variable, then the user-level
// default.
//
// Only user-level paths are ever returned. Claude Code also reads a project-level
// .claude/settings.json, and that file is part of the repository, so writing it would
// commit a plaintext key; install must never reach it.
func RuntimeConfigPath(runtime Runtime, explicit string) (string, error) {
	if value := strings.TrimSpace(explicit); value != "" {
		absolute, err := filepath.Abs(value)
		if err != nil {
			return "", Wrapf(err, CODE_CONFIG_UNREADABLE, "cannot resolve %q: %v", explicit, err)
		}
		return absolute, nil
	}
	switch runtime {
	case RuntimeClaude:
		if directory := strings.TrimSpace(os.Getenv(ENV_CLAUDE_CONFIG_DIR)); directory != "" {
			absolute, err := filepath.Abs(filepath.Join(directory, CLAUDE_SETTINGS_FILE))
			if err != nil {
				return "", Wrapf(err, CODE_CONFIG_UNREADABLE, "cannot resolve %s: %v", ENV_CLAUDE_CONFIG_DIR, err)
			}
			return absolute, nil
		}
		return userConfigPath(filepath.Join(CLAUDE_CONFIG_DIRNAME, CLAUDE_SETTINGS_FILE))
	case RuntimeCodex:
		if directory := strings.TrimSpace(os.Getenv(ENV_CODEX_HOME)); directory != "" {
			absolute, err := filepath.Abs(filepath.Join(directory, CODEX_CONFIG_FILE))
			if err != nil {
				return "", Wrapf(err, CODE_CONFIG_UNREADABLE, "cannot resolve %s: %v", ENV_CODEX_HOME, err)
			}
			return absolute, nil
		}
		return userConfigPath(filepath.Join(CODEX_CONFIG_DIRNAME, CODEX_CONFIG_FILE))
	case RuntimeOpenCode:
		if value := strings.TrimSpace(os.Getenv(ENV_OPENCODE_CONFIG)); value != "" {
			absolute, err := filepath.Abs(value)
			if err != nil {
				return "", Wrapf(err, CODE_CONFIG_UNREADABLE, "cannot resolve %s: %v", ENV_OPENCODE_CONFIG, err)
			}
			return absolute, nil
		}
		return userConfigPath(filepath.Join(OPENCODE_CONFIG_DIR, OPENCODE_CONFIG_FILE))
	default:
		return "", Errorf(CODE_RUNTIME_UNKNOWN, "unknown runtime %q", runtime)
	}
}

// userConfigPath joins a relative path to the user's home directory.
func userConfigPath(relative string) (string, error) {
	userHome, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(userHome) == "" {
		return "", Wrapf(err, CODE_CONFIG_UNREADABLE, "cannot determine the user home directory; pass --config <path>")
	}
	absolute, err := filepath.Abs(filepath.Join(userHome, relative))
	if err != nil {
		return "", Wrapf(err, CODE_CONFIG_UNREADABLE, "cannot resolve %s: %v", relative, err)
	}
	return absolute, nil
}

// InstallOptions is one `key install` run.
type InstallOptions struct {
	Home    Home
	Runtime Runtime
	// Repo is the repository's state record: its identity names the provider and
	// decides which values this tool may overwrite.
	Repo RepoState
	// Key is the plaintext gateway key, in memory for this call only.
	Key string
	// BaseURL is the gateway root: no trailing slash, no trailing /v1.
	BaseURL    string
	ConfigPath string
	Replace    bool
	Now        time.Time
	// Warn receives the plaintext warning before anything is written. It is a writer
	// rather than a []string so the warning cannot be reordered after the write.
	Warn io.Writer
}

// InstallResult is what `key install` changed.
type InstallResult struct {
	ConfigPath string
	BackupPath string
	Changed    []string
	// Unchanged is true when the file already held exactly this content, so nothing
	// was written and no backup was taken.
	Unchanged bool
	// PreviousModelProvider is the Codex model_provider this install replaced, for the
	// configuration summary; nil means the file had none.
	PreviousModelProvider *string
}

// Install writes one repository's key into one runtime's configuration file.
//
// This is the only command that leaves the key on disk in plaintext, and it does so
// only when asked: nothing else in the flow calls it. The order is fixed by what a
// failure may destroy — check what the file says, refuse a slot this tool does not
// own, copy the file aside, write, read the result back, and restore on any mismatch.
func Install(options InstallOptions) (InstallResult, error) {
	if options.Runtime == "" {
		return InstallResult{}, Errorf(CODE_RUNTIME_UNKNOWN, "no runtime; pass --runtime claude, codex or opencode")
	}
	if strings.TrimSpace(options.Key) == "" {
		return InstallResult{}, Errorf(CODE_KEY_NOT_FOUND, "no key material; run `oh-my-cpa key gen` and `key accept` first")
	}
	repoID := strings.TrimSpace(options.Repo.Repo)
	if repoID == "" {
		return InstallResult{}, Errorf(CODE_REPO_REQUIRED, "no repository; pass --repo <org/repo>")
	}
	now := options.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	contentPath, err := RuntimeConfigPath(options.Runtime, options.ConfigPath)
	if err != nil {
		return InstallResult{}, err
	}
	before, existed, err := readExistingConfig(contentPath)
	if err != nil {
		return InstallResult{}, err
	}

	// The slot check decides whether this install may proceed at all, so it runs before
	// the backup: a refused install leaves no file behind, not even a copy.
	store, err := OpenStoreForUpdate(options.Home, 0)
	if err != nil {
		return InstallResult{}, err
	}
	defer store.Close()
	existingSlot, hasSlot := store.Runtime(options.Runtime)

	current, err := runtimeCurrentValueForSlot(options.Runtime, before)
	if err != nil {
		return InstallResult{}, err
	}
	fingerprint := KeyValueFingerprint(options.Key)
	if current != "" && KeyValueFingerprint(current) != fingerprint {
		if !options.Replace {
			if hasSlot && existingSlot.Repo != repoID {
				return InstallResult{}, Errorf(CODE_OWNED_BY_ANOTHER, "%s already holds a key for %s; install %s with --replace, or run `oh-my-cpa key purge --repo %s` first", contentPath, existingSlot.Repo, repoID, existingSlot.Repo)
			}
			return InstallResult{}, Errorf(CODE_CONFIG_CONFLICT, "%s already holds a key this tool did not install for %s; pass --replace to overwrite it, or remove it by hand", contentPath, repoID)
		}
	}

	previousProvider, err := codexPreviousModelProvider(options.Runtime, before, existingSlot)
	if err != nil {
		return InstallResult{}, err
	}
	edited, changed, err := applyRuntimeEdit(options.Runtime, before, options.Repo, options.BaseURL, options.Key)
	if err != nil {
		return InstallResult{}, err
	}

	result := InstallResult{ConfigPath: contentPath, PreviousModelProvider: previousProvider}
	if existed && bytes.Equal(edited, before) {
		result.Unchanged = true
		result.BackupPath = existingSlot.BackupPath
		if err := recordSlot(store, options.Runtime, RuntimeSlot{
			Repo:                  repoID,
			InstalledAt:           now,
			ConfigPath:            contentPath,
			BackupPath:            existingSlot.BackupPath,
			ValueFingerprint:      fingerprint,
			BackupContainsKey:     hasSlot && existingSlot.BackupContainsKey,
			PreviousModelProvider: previousProvider,
		}); err != nil {
			return InstallResult{}, err
		}
		return result, store.Save()
	}

	warnPlaintext(options.Warn, options.Runtime, contentPath, repoID)
	backupPath, backupContainsKey, err := backupConfig(options.Home, contentPath, before, existed, options.Key, now)
	if err != nil {
		return InstallResult{}, err
	}
	result.BackupPath = backupPath
	if backupPath != "" {
		fmt.Fprintf(writerOrDiscard(options.Warn), "oh-my-cpa: backup of the previous file: %s\n", backupPath)
	}

	if err := writeFileAtomic(contentPath, edited); err != nil {
		return InstallResult{}, Wrapf(err, CODE_INSTALL_FAILED, "cannot write %s; the previous file is unchanged", contentPath)
	}
	if verifyErr := verifyRuntimeEdit(options.Runtime, before, contentPath, options.Repo, options.BaseURL, options.Key); verifyErr != nil {
		restoreErr := restoreConfig(contentPath, before, existed, backupPath)
		if restoreErr != nil {
			return InstallResult{}, Errorf(CODE_INSTALL_FAILED, "wrote %s but the result did not read back correctly (%v), and restoring it also failed (%v); the backup is %s", contentPath, verifyErr, restoreErr, backupPath)
		}
		return InstallResult{}, Errorf(CODE_INSTALL_FAILED, "wrote %s but the result did not read back correctly (%v); the previous file was restored", contentPath, verifyErr)
	}

	if err := recordSlot(store, options.Runtime, RuntimeSlot{
		Repo:                  repoID,
		InstalledAt:           now,
		ConfigPath:            contentPath,
		BackupPath:            backupPath,
		ValueFingerprint:      fingerprint,
		BackupContainsKey:     backupContainsKey,
		PreviousModelProvider: previousProvider,
	}); err != nil {
		return InstallResult{}, err
	}
	if err := store.Save(); err != nil {
		return InstallResult{}, err
	}
	result.Changed = changed
	return result, nil
}

// recordSlot installs one runtime's slot in the store.
func recordSlot(store *Store, runtime Runtime, slot RuntimeSlot) error {
	return store.PutRuntime(runtime, slot)
}

// readExistingConfig reads a configuration file, reporting whether it existed.
//
// A symbolic link is refused rather than followed: a settings file linked into a
// dotfiles repository is a normal setup, and writing through it would put the
// plaintext key into version control, which is the one outcome the warning exists to
// prevent.
func readExistingConfig(path string) ([]byte, bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, Wrapf(err, CODE_CONFIG_UNREADABLE, "cannot read %s: %v", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, false, Errorf(CODE_CONFIG_CONFLICT, "%s is a symbolic link; install does not write through a link because its target may be tracked by another tool, so pass --config with the real file if you want that file changed", path)
	}
	if info.IsDir() {
		return nil, false, Errorf(CODE_CONFIG_NOT_JSON, "%s is a directory, not a configuration file", path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, false, Wrapf(err, CODE_CONFIG_UNREADABLE, "cannot read %s: %v", path, err)
	}
	return content, true, nil
}

// warnPlaintext tells the person what this command is about to leave behind, before it
// leaves it. It names the file, says the key survives a restart, and gives the one
// command that takes it back out.
func warnPlaintext(writer io.Writer, runtime Runtime, path, repoID string) {
	writer = writerOrDiscard(writer)
	fmt.Fprintf(writer, "warning: this writes the plaintext gateway key into %s\n", path)
	fmt.Fprintf(writer, "warning: that file keeps it after this process exits and after a restart; anything that reads or copies %s can use the key\n", path)
	fmt.Fprintf(writer, "warning: keep that file out of git, logs, chat and backups you share\n")
	fmt.Fprintf(writer, "warning: remove it again with `oh-my-cpa key purge --repo %s`, or `--runtime %s` to remove only this one\n", repoID, runtime)
}

// writerOrDiscard keeps a caller that passes no writer from turning a warning into a
// panic. The warning is still produced on every path that has a writer.
func writerOrDiscard(writer io.Writer) io.Writer {
	if writer == nil {
		return io.Discard
	}
	return writer
}

// backupConfig copies the configuration aside before it is rewritten.
//
// A missing file has nothing to copy, so it produces no backup; an existing file that
// cannot be copied stops the install, because the copy is the only way back if the
// rewrite goes wrong.
func backupConfig(home Home, configPath string, before []byte, existed bool, key string, now time.Time) (string, bool, error) {
	if !existed {
		return "", false, nil
	}
	backupPath, err := home.BackupPath(configPath, now)
	if err != nil {
		return "", false, err
	}
	file, err := createExclusiveFile(backupPath)
	if err != nil {
		return "", false, Wrapf(err, CODE_BACKUP_FAILED, "cannot create the backup %s: %v", backupPath, err)
	}
	writeErr := func() error {
		if _, err := file.Write(before); err != nil {
			return err
		}
		return file.Sync()
	}()
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(backupPath)
		cause := writeErr
		if cause == nil {
			cause = closeErr
		}
		return "", false, Wrapf(cause, CODE_BACKUP_FAILED, "cannot write the backup %s: %v", backupPath, cause)
	}
	if err := restrictFile(backupPath); err != nil {
		_ = os.Remove(backupPath)
		return "", false, err
	}
	syncDirectory(home.BackupsDir())
	return backupPath, bytes.Contains(before, []byte(key)), nil
}

// restoreConfig puts the file back the way it was: the backup's bytes, or removal when
// the install created the file.
func restoreConfig(configPath string, before []byte, existed bool, backupPath string) error {
	if !existed {
		if err := os.Remove(configPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if backupPath == "" {
		return writeFileAtomic(configPath, before)
	}
	restored, err := os.ReadFile(backupPath)
	if err != nil {
		return err
	}
	return writeFileAtomic(configPath, restored)
}

// runtimeCurrentValueForSlot returns the gateway key the file currently holds for this
// tool's slot, or "" when the slot is free.
//
// Install and purge both read the slot through this function, so the value one writes
// and the value the other compares cannot disagree about where the key lives.
func runtimeCurrentValueForSlot(runtime Runtime, content []byte) (string, error) {
	if len(bytes.TrimSpace(content)) == 0 {
		return "", nil
	}
	switch runtime {
	case RuntimeClaude:
		object, err := parseConfigObject(content)
		if err != nil {
			return "", err
		}
		env, err := object.child("env")
		if err != nil {
			return "", Wrapf(err, CODE_CONFIG_CONFLICT, "the env member is not an object; fix it by hand")
		}
		value, _, err := env.stringValue(CLAUDE_ENV_KEY)
		if err != nil {
			return "", Wrapf(err, CODE_CONFIG_CONFLICT, "env.%s is not a string; fix it by hand", CLAUDE_ENV_KEY)
		}
		return value, nil
	case RuntimeOpenCode:
		object, err := parseConfigObject(content)
		if err != nil {
			return "", err
		}
		provider, err := object.child("provider")
		if err != nil {
			return "", Wrapf(err, CODE_CONFIG_CONFLICT, "the provider member is not an object; fix it by hand")
		}
		entry, err := provider.child(OPENCODE_PROVIDER_ID)
		if err != nil {
			return "", Wrapf(err, CODE_CONFIG_CONFLICT, "provider.%s is not an object; fix it by hand", OPENCODE_PROVIDER_ID)
		}
		options, err := entry.child("options")
		if err != nil {
			return "", Wrapf(err, CODE_CONFIG_CONFLICT, "provider.%s.options is not an object; fix it by hand", OPENCODE_PROVIDER_ID)
		}
		value, _, err := options.stringValue("apiKey")
		if err != nil {
			return "", Wrapf(err, CODE_CONFIG_CONFLICT, "provider.%s.options.apiKey is not a string; fix it by hand", OPENCODE_PROVIDER_ID)
		}
		return value, nil
	case RuntimeCodex:
		document, err := parseConfig(content)
		if err != nil {
			return "", err
		}
		start, end, found, err := document.blockMarkerPositions()
		if err != nil {
			return "", Wrapf(err, CODE_CONFIG_CONFLICT, "%s is not usable: %v", CODEX_CONFIG_FILE, err)
		}
		if !found {
			return "", nil
		}
		return document.blockValues(start, end)["experimental_bearer_token"], nil
	default:
		return "", Errorf(CODE_RUNTIME_UNKNOWN, "unknown runtime %q", runtime)
	}
}

// parseConfigObject parses a JSON configuration, reporting a parse failure as
// config_not_json and never rewriting the file.
//
// JSONC and TOML both fail here, which is the point: this tool will not silently
// discard comments or keys it does not model.
func parseConfigObject(content []byte) (*jsonObject, error) {
	object, err := parseJSONObject(content)
	if err != nil {
		return nil, Wrapf(err, CODE_CONFIG_NOT_JSON, "the file is not plain JSON (%v); it may be JSONC, so it is left untouched — remove comments and retry, or edit it by hand", err)
	}
	return object, nil
}

// parseConfig parses a Codex configuration. A file this editor cannot follow is
// config_conflict: multi-line strings and unreadable headers mean the line-level model
// no longer describes the file, and guessing a boundary would splice a managed block
// into the middle of somebody's value.
func parseConfig(content []byte) (*tomlDocument, error) {
	document, err := parseTOML(content)
	if err != nil {
		return nil, Wrapf(err, CODE_CONFIG_CONFLICT, "the file cannot be edited line by line (%v); it is left untouched — fix it or edit it by hand", err)
	}
	return document, nil
}

// codexPreviousModelProvider is the Codex model_provider an install is about to
// replace. A reinstall over this tool's own value keeps the original, so purge can
// still restore what the person had before the first install.
func codexPreviousModelProvider(runtime Runtime, content []byte, existingSlot RuntimeSlot) (*string, error) {
	if runtime != RuntimeCodex {
		return nil, nil
	}
	if len(bytes.TrimSpace(content)) == 0 {
		return existingSlot.PreviousModelProvider, nil
	}
	document, err := parseConfig(content)
	if err != nil {
		return nil, err
	}
	_, value, found, err := document.topLevelAssignment(CODEX_MODEL_PROVIDER)
	if err != nil {
		return nil, Wrapf(err, CODE_CONFIG_CONFLICT, "the top-level %s line cannot be read: %v", CODEX_MODEL_PROVIDER, err)
	}
	switch {
	case found && value != CODEX_PROVIDER_ID:
		previous := value
		return &previous, nil
	case found && value == CODEX_PROVIDER_ID:
		return existingSlot.PreviousModelProvider, nil
	default:
		return nil, nil
	}
}

// applyRuntimeEdit returns the file content with this tool's slot written, and the
// names of the members that changed.
func applyRuntimeEdit(runtime Runtime, before []byte, record RepoState, baseURL, key string) ([]byte, []string, error) {
	baseURL = strings.TrimSuffix(baseURL, "/")
	switch runtime {
	case RuntimeClaude:
		object, err := parseEmptyOrConfigObject(before)
		if err != nil {
			return nil, nil, err
		}
		env, err := object.child("env")
		if err != nil {
			return nil, nil, Wrapf(err, CODE_CONFIG_CONFLICT, "the env member is not an object; fix it by hand")
		}
		if env.has(CLAUDE_ENV_API_KEY) {
			return nil, nil, Errorf(CODE_CONFIG_CONFLICT, "env.%s is already set in the file; it can take precedence over %s, so remove it by hand first", CLAUDE_ENV_API_KEY, CLAUDE_ENV_KEY)
		}
		if err := env.set(CLAUDE_ENV_BASE_URL, baseURL); err != nil {
			return nil, nil, err
		}
		if err := env.set(CLAUDE_ENV_KEY, key); err != nil {
			return nil, nil, err
		}
		if err := object.setChild("env", env); err != nil {
			return nil, nil, err
		}
		encoded, err := object.encodeIndent()
		if err != nil {
			return nil, nil, Wrapf(err, CODE_INSTALL_FAILED, "cannot encode the settings file: %v", err)
		}
		return encoded, []string{"env." + CLAUDE_ENV_BASE_URL, "env." + CLAUDE_ENV_KEY}, nil
	case RuntimeOpenCode:
		object, err := parseEmptyOrConfigObject(before)
		if err != nil {
			return nil, nil, err
		}
		provider, err := object.child("provider")
		if err != nil {
			return nil, nil, Wrapf(err, CODE_CONFIG_CONFLICT, "the provider member is not an object; fix it by hand")
		}
		entry, err := provider.child(OPENCODE_PROVIDER_ID)
		if err != nil {
			return nil, nil, Wrapf(err, CODE_CONFIG_CONFLICT, "provider.%s is not an object; fix it by hand", OPENCODE_PROVIDER_ID)
		}
		options, err := entry.child("options")
		if err != nil {
			return nil, nil, Wrapf(err, CODE_CONFIG_CONFLICT, "provider.%s.options is not an object; fix it by hand", OPENCODE_PROVIDER_ID)
		}
		if err := entry.set("npm", OPENCODE_NPM); err != nil {
			return nil, nil, err
		}
		if err := entry.set("name", "Oh My CPA ("+record.Repo+")"); err != nil {
			return nil, nil, err
		}
		if err := options.set("baseURL", baseURL+"/v1"); err != nil {
			return nil, nil, err
		}
		if err := options.set("apiKey", key); err != nil {
			return nil, nil, err
		}
		if err := entry.setChild("options", options); err != nil {
			return nil, nil, err
		}
		if err := provider.setChild(OPENCODE_PROVIDER_ID, entry); err != nil {
			return nil, nil, err
		}
		if err := object.setChild("provider", provider); err != nil {
			return nil, nil, err
		}
		encoded, err := object.encodeIndent()
		if err != nil {
			return nil, nil, Wrapf(err, CODE_INSTALL_FAILED, "cannot encode the provider file: %v", err)
		}
		return encoded, []string{"provider." + OPENCODE_PROVIDER_ID}, nil
	case RuntimeCodex:
		document, err := parseEmptyOrConfig(before)
		if err != nil {
			return nil, nil, err
		}
		start, end, found, err := document.blockMarkerPositions()
		if err != nil {
			return nil, nil, Wrapf(err, CODE_CONFIG_CONFLICT, "the managed block in the file is damaged: %v; repair it by hand", err)
		}
		index, collides, scanErr := document.unmanagedProviderTable(start, end, found)
		if scanErr != nil {
			return nil, nil, Wrapf(scanErr, CODE_CONFIG_CONFLICT, "cannot read the tables in the file: %v", scanErr)
		}
		if collides {
			return nil, nil, Errorf(CODE_CONFIG_CONFLICT, "line %d defines [model_providers.%s] outside this tool's managed block; move or remove it first, because two tables with one name make the file invalid TOML", index+1, CODEX_PROVIDER_ID)
		}
		if found {
			document.removeBlock(start, end)
		}
		document.appendBlock(codexManagedBlock(record, baseURL, key))
		if err := document.setTopLevelKey(CODEX_MODEL_PROVIDER, CODEX_PROVIDER_ID); err != nil {
			return nil, nil, Wrapf(err, CODE_CONFIG_CONFLICT, "cannot place the top-level %s line: %v", CODEX_MODEL_PROVIDER, err)
		}
		return document.encode(), []string{CODEX_MODEL_PROVIDER, "[model_providers." + CODEX_PROVIDER_ID + "]"}, nil
	default:
		return nil, nil, Errorf(CODE_RUNTIME_UNKNOWN, "unknown runtime %q", runtime)
	}
}

// parseEmptyOrConfigObject treats an absent file as an empty object.
func parseEmptyOrConfigObject(content []byte) (*jsonObject, error) {
	if len(bytes.TrimSpace(content)) == 0 {
		return &jsonObject{values: map[string]json.RawMessage{}}, nil
	}
	return parseConfigObject(content)
}

// parseEmptyOrConfig treats an absent Codex configuration as an empty document.
func parseEmptyOrConfig(content []byte) (*tomlDocument, error) {
	if len(bytes.TrimSpace(content)) == 0 {
		return &tomlDocument{finalNewline: true}, nil
	}
	return parseConfig(content)
}

// verifyRuntimeEdit re-reads the written file and checks two things: this tool's slot
// now holds this repository's values, and everything the file held before is still
// there. A failure is what triggers the restore.
func verifyRuntimeEdit(runtime Runtime, before []byte, path string, record RepoState, baseURL, key string) error {
	after, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	current, err := runtimeCurrentValueForSlot(runtime, after)
	if err != nil {
		return err
	}
	if current != key {
		return errors.New("the key this tool wrote is not what the file now holds")
	}
	switch runtime {
	case RuntimeClaude:
		beforeObject, err := parseEmptyOrConfigObject(before)
		if err != nil {
			return err
		}
		afterObject, err := parseEmptyOrConfigObject(after)
		if err != nil {
			return err
		}
		written := []string{
			"env." + CLAUDE_ENV_BASE_URL,
			"env." + CLAUDE_ENV_KEY,
		}
		if err := verifyUnwrittenJSONMembers(beforeObject, afterObject, written); err != nil {
			return err
		}
		return verifyClaudeBaseURL(afterObject, strings.TrimSuffix(baseURL, "/"))
	case RuntimeOpenCode:
		beforeObject, err := parseEmptyOrConfigObject(before)
		if err != nil {
			return err
		}
		afterObject, err := parseEmptyOrConfigObject(after)
		if err != nil {
			return err
		}
		prefix := "provider." + OPENCODE_PROVIDER_ID
		written := []string{
			prefix + ".npm",
			prefix + ".name",
			prefix + ".options.baseURL",
			prefix + ".options.apiKey",
		}
		if err := verifyUnwrittenJSONMembers(beforeObject, afterObject, written); err != nil {
			return err
		}
		return verifyOpenCodeBaseURL(afterObject, strings.TrimSuffix(baseURL, "/"))
	case RuntimeCodex:
		return verifyCodexEdit(before, after, record, strings.TrimSuffix(baseURL, "/")+"/v1", key)
	default:
		return Errorf(CODE_RUNTIME_UNKNOWN, "unknown runtime %q", runtime)
	}
}

// verifyUnwrittenJSONMembers proves the edit stayed inside its own slot: every leaf
// the file held before is either one of the paths this tool wrote, or still holds the
// same value afterwards.
//
// The comparison is on leaves rather than top-level members because both slots are
// nested: Claude's settings file typically holds other env entries, and an OpenCode
// file holds the operator's other providers. Comparing only the outermost member would
// let an edit silently drop them.
func verifyUnwrittenJSONMembers(before, after *jsonObject, written []string) error {
	skip := make(map[string]bool, len(written))
	for _, path := range written {
		skip[path] = true
	}
	beforeLeaves, err := jsonLeaves(before, "")
	if err != nil {
		return err
	}
	afterLeaves, err := jsonLeaves(after, "")
	if err != nil {
		return err
	}
	for path, value := range beforeLeaves {
		if skip[path] {
			continue
		}
		afterValue, ok := afterLeaves[path]
		if !ok {
			return fmt.Errorf("the member %s disappeared", path)
		}
		if !sameJSON(value, afterValue) {
			return fmt.Errorf("the member %s changed", path)
		}
	}
	return nil
}

// jsonLeaves flattens an object into dotted leaf paths. An empty object contributes no
// leaf, so an install that fills in a previously empty env object is not a change to
// anything that existed.
func jsonLeaves(object *jsonObject, prefix string) (map[string]json.RawMessage, error) {
	leaves := map[string]json.RawMessage{}
	for _, key := range object.keys {
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		raw := bytes.TrimSpace(object.values[key])
		if len(raw) > 0 && raw[0] == '{' {
			nested, err := parseJSONObject(raw)
			if err != nil {
				return nil, err
			}
			nestedLeaves, err := jsonLeaves(nested, path)
			if err != nil {
				return nil, err
			}
			for nestedPath, nestedValue := range nestedLeaves {
				leaves[nestedPath] = nestedValue
			}
			continue
		}
		leaves[path] = object.values[key]
	}
	return leaves, nil
}

// verifyClaudeBaseURL confirms the base URL member holds the gateway root.
func verifyClaudeBaseURL(object *jsonObject, baseURL string) error {
	env, err := object.child("env")
	if err != nil {
		return err
	}
	value, found, err := env.stringValue(CLAUDE_ENV_BASE_URL)
	if err != nil || !found {
		return errors.New("the base URL member did not survive the write")
	}
	if value != baseURL {
		return errors.New("the base URL member does not hold the gateway root")
	}
	return nil
}

// verifyOpenCodeBaseURL confirms the provider's base URL ends at /v1 under the root.
func verifyOpenCodeBaseURL(object *jsonObject, baseURL string) error {
	provider, err := object.child("provider")
	if err != nil {
		return err
	}
	entry, err := provider.child(OPENCODE_PROVIDER_ID)
	if err != nil {
		return err
	}
	options, err := entry.child("options")
	if err != nil {
		return err
	}
	value, found, err := options.stringValue("baseURL")
	if err != nil || !found {
		return errors.New("the provider base URL did not survive the write")
	}
	if value != baseURL+"/v1" {
		return errors.New("the provider base URL does not point at the gateway")
	}
	return nil
}

// verifyCodexEdit checks the managed block holds this repository's values and that
// every line outside it is byte-identical to before.
func verifyCodexEdit(before, after []byte, record RepoState, baseURL, key string) error {
	afterDocument, err := parseConfig(after)
	if err != nil {
		return err
	}
	start, end, found, err := afterDocument.blockMarkerPositions()
	if err != nil {
		return err
	}
	if !found {
		return errors.New("the managed block is missing after the write")
	}
	values := afterDocument.blockValues(start, end)
	expected := map[string]string{
		"name":                      "Oh My CPA (" + record.Repo + ")",
		"base_url":                  baseURL,
		"wire_api":                  CODEX_WIRE_API,
		"experimental_bearer_token": key,
	}
	for name, want := range expected {
		if values[name] != want {
			return fmt.Errorf("the managed block's %s is not what this tool wrote", name)
		}
	}
	_, provider, providerFound, err := afterDocument.topLevelAssignment(CODEX_MODEL_PROVIDER)
	if err != nil {
		return err
	}
	if !providerFound || provider != CODEX_PROVIDER_ID {
		return errors.New("the top-level model_provider is not set to this tool's provider")
	}
	beforeDocument, err := parseEmptyOrConfig(before)
	if err != nil {
		return err
	}
	return verifyCodexOutsideBlock(beforeDocument, afterDocument, start, end)
}

// verifyCodexOutsideBlock compares every line the managed block does not cover.
func verifyCodexOutsideBlock(before, after *tomlDocument, start, end int) error {
	beforeLines := withoutCodexManagedRegion(before)
	afterLines := withoutCodexManagedRegion(after, start, end)
	if len(beforeLines) != len(afterLines) {
		return errors.New("the number of lines outside the managed block changed")
	}
	for index := range beforeLines {
		if beforeLines[index] != afterLines[index] {
			return fmt.Errorf("line %q outside the managed block changed", strings.TrimSpace(beforeLines[index]))
		}
	}
	return nil
}

// withoutCodexManagedRegion returns the document's lines minus the managed block and
// minus the top-level model_provider line, the two regions this tool owns.
func withoutCodexManagedRegion(document *tomlDocument, span ...int) []string {
	start, end := -1, -1
	if len(span) == 2 {
		start, end = span[0], span[1]
	} else if detectedStart, detectedEnd, found, err := document.blockMarkerPositions(); err == nil && found {
		start, end = detectedStart, detectedEnd
	}
	scanned, err := document.layout()
	if err != nil {
		return nil
	}
	kept := make([]string, 0, len(document.lines))
	for index, line := range document.lines {
		if start >= 0 && index >= start && index <= end {
			continue
		}
		if scanned[index].topLevel {
			if key, _, ok := splitTOMLAssignment(scanned[index].code); ok && key == CODEX_MODEL_PROVIDER {
				continue
			}
		}
		kept = append(kept, line)
	}
	// Blank lines at the edges are formatting, not content: an install adds one before
	// the block and deletes it again, so they must not count as a change.
	for len(kept) > 0 && strings.TrimSpace(kept[0]) == "" {
		kept = kept[1:]
	}
	for len(kept) > 0 && strings.TrimSpace(kept[len(kept)-1]) == "" {
		kept = kept[:len(kept)-1]
	}
	return kept
}

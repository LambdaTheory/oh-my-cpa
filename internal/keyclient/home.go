package keyclient

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/oh-my-cpa/oh-my-cpa/internal/repokey"
)

const (
	// ENV_HOME overrides where keys, envelopes and state live. One name, one default:
	// the requirement and every document use OMC_HOME, so nothing else is read.
	ENV_HOME = "OMC_HOME"

	// DEFAULT_HOME_DIRNAME is the directory under the user's home directory.
	DEFAULT_HOME_DIRNAME = ".omc"

	// STATE_VERSION and CONFIG_VERSION are the on-disk schema versions. A file with
	// another version is refused rather than migrated: the layout is young and a
	// silent upgrade could hide a corrupt write.
	STATE_VERSION  = 1
	CONFIG_VERSION = 1

	// DIR_MODE and FILE_MODE are the Unix modes for everything this package creates.
	// Windows has no equivalent; home_windows.go applies a DACL instead.
	DIR_MODE  os.FileMode = 0o700
	FILE_MODE os.FileMode = 0o600

	// SLUG_MAX_BYTES caps the readable part of a filename so a long repository
	// identity cannot exceed common filesystem limits once the directories, the
	// hash and the extension are added.
	SLUG_MAX_BYTES = 80

	KEYS_DIR      = "keys"
	ENVELOPES_DIR = "envelopes"
	BACKUPS_DIR   = "backups"
	STATE_FILE    = "state.json"
	CONFIG_FILE   = "config.json"
	LOCK_FILE     = ".lock"
)

// Home is $OMC_HOME: the directory that holds this machine's private keys,
// delivery envelopes, backups and state.
type Home struct {
	// Root is an absolute path. ResolveHome produces it; nothing here accepts a
	// relative root, because a command may run from any working directory.
	Root string
}

// ResolveHome picks the home directory: the --home flag, then OMC_HOME, then
// ~/.omc. A relative value is resolved against the current working directory. It
// does not create anything; a read-only command should not have to.
func ResolveHome(flagValue string) (Home, error) {
	value := strings.TrimSpace(flagValue)
	if value == "" {
		value = strings.TrimSpace(os.Getenv(ENV_HOME))
	}
	if value == "" {
		userHome, err := os.UserHomeDir()
		if err != nil || strings.TrimSpace(userHome) == "" {
			return Home{}, Wrapf(err, CODE_HOME_UNAVAILABLE, "cannot determine the user home directory; set %s or pass --home", ENV_HOME)
		}
		value = filepath.Join(userHome, DEFAULT_HOME_DIRNAME)
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return Home{}, Wrapf(err, CODE_HOME_UNAVAILABLE, "cannot resolve %q: %v", value, err)
	}
	return Home{Root: absolute}, nil
}

// Ensure creates the home directory and its subdirectories and refuses to continue
// when they are not restricted to this user.
//
// A directory this call creates is given owner-only permissions. A directory that
// already exists is only inspected: tightening someone else's directory silently would
// hide that it was open, and a mount this process cannot chmod would then appear fine
// until the first key write. Group- or world-writable is reported as home_insecure with
// the chmod that fixes it.
//
// The check is on writability, not readability: a read-only mount is a legitimate home,
// and the write that follows fails on its own terms.
func (h Home) Ensure() error {
	if strings.TrimSpace(h.Root) == "" {
		return Errorf(CODE_HOME_UNAVAILABLE, "no home directory; set %s or pass --home", ENV_HOME)
	}
	for _, directory := range []string{h.Root, filepath.Join(h.Root, KEYS_DIR), filepath.Join(h.Root, ENVELOPES_DIR), filepath.Join(h.Root, BACKUPS_DIR)} {
		if err := prepareDirectory(directory); err != nil {
			return err
		}
	}
	return nil
}

// prepareDirectory creates a missing directory with owner-only permissions, or verifies
// an existing one without changing it.
func prepareDirectory(path string) error {
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := os.MkdirAll(path, DIR_MODE); err != nil {
			return Wrapf(err, CODE_HOME_UNAVAILABLE, "cannot create %s: %v", path, err)
		}
		return restrictDirectory(path)
	case err != nil:
		return Wrapf(err, CODE_HOME_UNAVAILABLE, "cannot read %s: %v", path, err)
	case !info.IsDir():
		return Errorf(CODE_HOME_UNAVAILABLE, "%s is not a directory", path)
	}
	return verifyDirectory(path)
}

// CheckReadableHome verifies an existing home's permissions without creating
// anything, so a read-only command reports an insecure home instead of silently
// reading keys another user could have replaced.
func (h Home) CheckReadableHome() error {
	info, err := os.Stat(h.Root)
	if err != nil {
		if os.IsNotExist(err) {
			return Errorf(CODE_HOME_UNAVAILABLE, "%s does not exist; run `oh-my-cpa key gen --repo <repo>` to create it", h.Root)
		}
		return Wrapf(err, CODE_HOME_UNAVAILABLE, "cannot read %s: %v", h.Root, err)
	}
	if !info.IsDir() {
		return Errorf(CODE_HOME_UNAVAILABLE, "%s is not a directory", h.Root)
	}
	return verifyDirectory(h.Root)
}

// StatePath is the absolute path of state.json.
func (h Home) StatePath() string { return filepath.Join(h.Root, STATE_FILE) }

// ConfigPath is the absolute path of config.json.
func (h Home) ConfigPath() string { return filepath.Join(h.Root, CONFIG_FILE) }

// LockPath is the absolute path of the per-home write lock.
func (h Home) LockPath() string { return filepath.Join(h.Root, LOCK_FILE) }

// KeysDir and EnvelopesDir are the absolute directories that hold the files.
func (h Home) KeysDir() string      { return filepath.Join(h.Root, KEYS_DIR) }
func (h Home) EnvelopesDir() string { return filepath.Join(h.Root, ENVELOPES_DIR) }
func (h Home) BackupsDir() string   { return filepath.Join(h.Root, BACKUPS_DIR) }

// KeyRelPath and EnvelopeRelPath are the relative paths recorded in state.json.
func (h Home) KeyRelPath(repoID string) (string, error) {
	stem, err := fileStem(repoID)
	if err != nil {
		return "", err
	}
	return KEYS_DIR + "/" + stem + ".key", nil
}

func (h Home) EnvelopeRelPath(repoID string) (string, error) {
	stem, err := fileStem(repoID)
	if err != nil {
		return "", err
	}
	return ENVELOPES_DIR + "/" + stem + ".json", nil
}

// KeyPath and EnvelopePath are the absolute paths of a repository's files.
func (h Home) KeyPath(repoID string) (string, error) {
	relative, err := h.KeyRelPath(repoID)
	if err != nil {
		return "", err
	}
	return filepath.Join(h.Root, filepath.FromSlash(relative)), nil
}

func (h Home) EnvelopePath(repoID string) (string, error) {
	relative, err := h.EnvelopeRelPath(repoID)
	if err != nil {
		return "", err
	}
	return filepath.Join(h.Root, filepath.FromSlash(relative)), nil
}

// BackupPath names a backup of configPath taken at `at`: a UTC timestamp followed
// by the flattened absolute path, so backups of two files stay distinguishable.
// An existing name gets -2, -3, … so a retry never overwrites an earlier backup.
func (h Home) BackupPath(configPath string, at time.Time) (string, error) {
	flattened := flattenPath(configPath)
	stem := at.UTC().Format("20060102T150405Z") + "-" + flattened
	extension := filepath.Ext(configPath)
	if extension == "" || len(extension) > 8 {
		extension = ".bak"
	}
	base := strings.TrimSuffix(stem, extension)
	candidate := filepath.Join(h.BackupsDir(), base+extension)
	for attempt := 2; ; attempt++ {
		if _, err := os.Lstat(candidate); os.IsNotExist(err) {
			return candidate, nil
		} else if err != nil {
			return "", Wrapf(err, CODE_BACKUP_FAILED, "cannot inspect %s: %v", candidate, err)
		}
		candidate = filepath.Join(h.BackupsDir(), fmt.Sprintf("%s-%d%s", base, attempt, extension))
		if attempt > 999 {
			return "", Errorf(CODE_BACKUP_FAILED, "cannot find an unused backup name in %s", h.BackupsDir())
		}
	}
}

// createExclusiveFile creates path with owner-only permissions and fails when it already
// exists. O_EXCL is what keeps two concurrent writers from silently sharing a key.
func createExclusiveFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, FILE_MODE)
}

// RepoLabel is the one-line label messages use for a repository: the spelling a person
// submitted, with the normalized identity beside it when the two differ.
//
// The identity is what the console records, so a message that shows only the display
// spelling leaves a reader unable to match it against an application; a message that
// shows only the identity loses the capitalization the person wrote.
func RepoLabel(record RepoState) string {
	display := strings.TrimSpace(record.RepoDisplay)
	if display == "" || display == record.Repo {
		return record.Repo
	}
	return fmt.Sprintf("%s (%s)", display, record.Repo)
}

// flattenPath turns an absolute configuration path into a filename-safe token:
// separators and any character outside [A-Za-z0-9._-] become '-'. The result is not
// reversed; a backup's origin is also recorded in state.
func flattenPath(path string) string {
	var builder strings.Builder
	for _, r := range path {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			builder.WriteRune(r)
		default:
			builder.WriteByte('-')
		}
	}
	value := strings.Trim(builder.String(), "-")
	if value == "" {
		value = "config"
	}
	if len(value) > 120 {
		value = value[len(value)-120:]
	}
	return value
}

// fileStem is the filename without extension: a percent-encoded slug plus an eight
// hex character hash of the exact repository identity.
//
// The hash is what makes the mapping safe on case-insensitive and normalizing
// filesystems: "Org/Repo" and "org/repo" produce the same identity upstream, but a
// hostile or merely unusual spelling can differ in case, percent-encoding or
// truncation, and the hash keeps those apart.
func fileStem(repoID string) (string, error) {
	repoID = strings.TrimSpace(repoID)
	if repoID == "" {
		return "", Errorf(CODE_REPO_REQUIRED, "no repository identity")
	}
	digest := sha256.Sum256([]byte(repoID))
	return fileSlug(repoID) + "-" + hex.EncodeToString(digest[:])[:8], nil
}

// fileSlug is the readable half of a filename: the repository identity with everything
// unsafe percent-encoded, Windows device names escaped, trailing dots and spaces
// removed, and the length capped.
//
// Trailing dots and spaces are stripped because Windows silently drops them, which
// would make two distinct identities resolve to the same file.
func fileSlug(repoID string) string {
	slug := percentEncode(strings.TrimSpace(repoID))
	if isReservedWindowsName(slug) {
		slug = "_" + slug
	}
	slug = strings.TrimRight(slug, ". ")
	slug = truncateBytes(slug, SLUG_MAX_BYTES)
	if slug == "" {
		slug = "repo"
	}
	return slug
}

// percentEncode keeps the characters that are safe and readable in a filename and
// percent-encodes every other UTF-8 byte. Uppercase hex matches URL conventions and
// keeps the mapping deterministic.
func percentEncode(value string) string {
	const hexadecimal = "0123456789ABCDEF"
	var builder strings.Builder
	for index := 0; index < len(value); index++ {
		b := value[index]
		switch {
		case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9', b == '.', b == '_', b == '-':
			builder.WriteByte(b)
		default:
			builder.WriteByte('%')
			builder.WriteByte(hexadecimal[b>>4])
			builder.WriteByte(hexadecimal[b&0x0F])
		}
	}
	return builder.String()
}

// reservedWindowsNames are the device names Windows refuses as filenames, in every
// directory and with any extension.
var reservedWindowsNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// isReservedWindowsName reports whether slug is a reserved device name, optionally
// followed by an extension ("con.txt" is reserved too).
func isReservedWindowsName(slug string) bool {
	base := slug
	if dot := strings.Index(base, "."); dot >= 0 {
		base = base[:dot]
	}
	return reservedWindowsNames[strings.ToUpper(base)]
}

// truncateBytes shortens value to at most limit bytes without splitting a rune, so
// a UTF-8 repository name still produces a valid filename.
func truncateBytes(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut]
}

// DeriveRepoID normalizes a repository identity the way the server does.
//
// A path-shaped value is refused rather than reduced to its last segment: `--repo` names
// an identity, and silently turning "../../../etc/passwd" into "passwd" or "a/../../b"
// into an identity that still carries the traversal segments would select a key the
// person did not name. Inference from a working directory goes through the directory's
// base name instead.
func DeriveRepoID(value string) (string, string, error) {
	if looksLikePath(value) {
		return "", "", Errorf(CODE_REPO_INVALID, "repository %q is a path, not an identity; pass org/repo, a clone URL, or a bare directory name", value)
	}
	repoID, _, err := repokey.NormalizeRepo(value)
	if err != nil {
		if strings.Contains(err.Error(), "repo_required") {
			return "", "", Errorf(CODE_REPO_REQUIRED, "no repository; pass --repo <org/repo> or run from a repository directory")
		}
		return "", "", Errorf(CODE_REPO_INVALID, "repository %q is not a valid identity; use org/repo", value)
	}
	display, displayErr := repokey.RepoDisplay(value)
	if displayErr != nil || strings.TrimSpace(display) == "" {
		display = repoID
	}
	return repoID, display, nil
}

// looksLikePath reports whether a repository value is a filesystem path rather than an
// identity. A bare directory name is still a valid local identity; what is refused is a
// spelling that only makes sense as a path — an absolute or drive-qualified one, or one
// with ".." segments.
func looksLikePath(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	for _, prefix := range []string{"/", "\\", "./", ".\\", "../", "..\\"} {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	if len(trimmed) >= 2 && trimmed[1] == ':' {
		drive := trimmed[0]
		if (drive >= 'A' && drive <= 'Z') || (drive >= 'a' && drive <= 'z') {
			return true
		}
	}
	for _, segment := range strings.FieldsFunc(trimmed, func(r rune) bool { return r == '/' || r == '\\' }) {
		if segment == ".." {
			return true
		}
	}
	return false
}

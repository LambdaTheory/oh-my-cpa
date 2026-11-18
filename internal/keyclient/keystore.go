package keyclient

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/repokey"
)

// PRIVATE_KEY_BYTES is the length of an X25519 private key. The file holds exactly
// these bytes and nothing else, so a truncated or padded file is a detectable error
// rather than a key that silently decrypts to garbage.
const PRIVATE_KEY_BYTES = 32

// GenerateKeyPair makes a fresh X25519 identity in memory. Writing it is a separate
// step so a failed write cannot leave a half-recorded key.
func GenerateKeyPair() (KeyPair, error) {
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return KeyPair{}, fmt.Errorf("generate X25519 key: %w", err)
	}
	raw := private.Bytes()
	public := private.PublicKey().Bytes()
	return KeyPair{
		PrivateKey:  raw,
		PublicKey:   PublicKeyText(public),
		Fingerprint: repokey.PublicKeyFingerprint(public),
	}, nil
}

// PublicKeyText is the text form a caller submits with repo_key_apply. It matches what
// repokey.ParsePublicKey accepts, so the printed line can be pasted into the call.
func PublicKeyText(publicKey []byte) string {
	return "x25519:" + base64.StdEncoding.EncodeToString(publicKey)
}

// KeyPairFromPrivate derives the public half, text form and fingerprint of an existing
// private key. It is how `key gen` repairs state for a key it did not create in this
// invocation.
func KeyPairFromPrivate(raw []byte) (KeyPair, error) {
	private, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		return KeyPair{}, Errorf(CODE_KEY_UNREADABLE, "the private key is not a valid X25519 key")
	}
	public := private.PublicKey().Bytes()
	copied := make([]byte, len(raw))
	copy(copied, raw)
	return KeyPair{
		PrivateKey:  copied,
		PublicKey:   PublicKeyText(public),
		Fingerprint: repokey.PublicKeyFingerprint(public),
	}, nil
}

// ReadPrivateKey loads a repository's private key from $OMC_HOME/keys/.
//
// A missing file is key_not_found with the command that would create one; a file of
// the wrong length is key_unreadable. Both leave the file alone: repairing a key by
// rewriting it would destroy the only copy of the half that decrypts existing
// envelopes.
func ReadPrivateKey(home Home, repoID string) ([]byte, error) {
	path, err := home.KeyPath(repoID)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, Errorf(CODE_KEY_NOT_FOUND, "no private key for %s; run `oh-my-cpa key gen --repo %s`", repoID, repoID)
		}
		return nil, Wrapf(err, CODE_KEY_UNREADABLE, "cannot read %s: %v", path, err)
	}
	if len(raw) != PRIVATE_KEY_BYTES {
		return nil, Errorf(CODE_KEY_UNREADABLE, "%s holds %d bytes, not %d; it is not an X25519 private key", path, len(raw), PRIVATE_KEY_BYTES)
	}
	if _, err := ecdh.X25519().NewPrivateKey(raw); err != nil {
		return nil, Errorf(CODE_KEY_UNREADABLE, "%s is not a valid X25519 private key", path)
	}
	return raw, nil
}

// HasPrivateKey reports whether a key file exists, without reading it.
func HasPrivateKey(home Home, repoID string) (bool, error) {
	path, err := home.KeyPath(repoID)
	if err != nil {
		return false, err
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, Wrapf(err, CODE_KEY_UNREADABLE, "cannot inspect %s: %v", path, err)
	}
	return info.Mode().IsRegular(), nil
}

// HasEnvelope reports whether a delivery envelope exists for the repository.
func HasEnvelope(home Home, repoID string) (bool, error) {
	path, err := home.EnvelopePath(repoID)
	if err != nil {
		return false, err
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, Wrapf(err, CODE_ENVELOPE_INVALID, "cannot inspect %s: %v", path, err)
	}
	return info.Mode().IsRegular(), nil
}

// WritePrivateKey stores a private key with owner-only permissions.
//
// The write is exclusive: an existing file is never truncated, so a repeated
// generation cannot destroy a key that existing envelopes were sealed to. The file is
// restricted after it is written and before it is reported written, because a
// permissive umask would otherwise leave the key readable for the moment between
// create and chmod.
func WritePrivateKey(home Home, repoID string, raw []byte) (string, error) {
	if len(raw) != PRIVATE_KEY_BYTES {
		return "", Errorf(CODE_KEY_UNREADABLE, "refusing to write a %d-byte private key", len(raw))
	}
	path, err := home.KeyPath(repoID)
	if err != nil {
		return "", err
	}
	file, err := createExclusiveFile(path)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return "", Errorf(CODE_KEY_EXISTS, "%s already exists; `key gen` reuses it, and replacing it needs `key purge` first", path)
		}
		return "", Wrapf(err, CODE_HOME_UNAVAILABLE, "cannot create %s: %v", path, err)
	}
	written := func() error {
		if _, err := file.Write(raw); err != nil {
			return err
		}
		return file.Sync()
	}()
	closeErr := file.Close()
	if written != nil || closeErr != nil {
		_ = os.Remove(path)
		if written != nil {
			return "", Wrapf(written, CODE_HOME_UNAVAILABLE, "cannot write %s: %v", path, written)
		}
		return "", Wrapf(closeErr, CODE_HOME_UNAVAILABLE, "cannot close %s: %v", path, closeErr)
	}
	if err := restrictFile(path); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	syncDirectory(home.KeysDir())
	return path, nil
}

// GenOptions is one `key gen` run.
type GenOptions struct {
	Home    Home
	Repo    string
	BaseURL string
	Now     time.Time
}

// GenResult reports what `key gen` did: created a key or found one, and the identity
// to submit.
type GenResult struct {
	Record      RepoState
	Pairs       KeyPair
	Reused      bool
	WroteConfig bool
}

// Generate creates or reuses a repository's key pair and records it in state.
//
// A key that already exists is reused and its public key recomputed, never replaced:
// rotating would make every envelope already delivered for this repository
// undecryptable, and the caller would have no way to tell that from a working key.
// Rotating is `key purge` followed by `key gen`.
func Generate(options GenOptions) (GenResult, error) {
	if strings.TrimSpace(options.Repo) == "" {
		return GenResult{}, Errorf(CODE_REPO_REQUIRED, "no repository; pass --repo <org/repo>")
	}
	repoID, display, err := DeriveRepoID(options.Repo)
	if err != nil {
		return GenResult{}, err
	}
	now := options.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	home := options.Home
	store, err := OpenStoreForUpdate(home, 0)
	if err != nil {
		return GenResult{}, err
	}
	defer store.Close()

	result := GenResult{}
	record, exists := store.Repo(repoID)
	if !exists {
		record = RepoState{
			Repo:           repoID,
			RepoDisplay:    display,
			ApprovalStatus: APPROVAL_NOT_APPLIED,
			StatusSource:   STATUS_SOURCE_GEN,
		}
	}
	// The record keeps the spelling the repository was first registered under, so a later
	// run from another clone does not rewrite the name a person recognises. An empty
	// display is filled in rather than left blank.
	if record.RepoDisplay == "" {
		record.RepoDisplay = display
	}
	keyRelative, err := home.KeyRelPath(repoID)
	if err != nil {
		return GenResult{}, err
	}

	existing, readErr := ReadPrivateKey(home, repoID)
	switch {
	case readErr == nil:
		pair, err := KeyPairFromPrivate(existing)
		if err != nil {
			return GenResult{}, err
		}
		result.Reused = true
		result.Pairs = pair
	case CodeOf(readErr) == CODE_KEY_NOT_FOUND:
		pair, err := GenerateKeyPair()
		if err != nil {
			return GenResult{}, Wrapf(err, CODE_HOME_UNAVAILABLE, "cannot generate a key pair: %v", err)
		}
		if _, err := WritePrivateKey(home, repoID, pair.PrivateKey); err != nil {
			return GenResult{}, err
		}
		result.Pairs = pair
		record.CreatedAt = now
	default:
		return GenResult{}, readErr
	}

	// Repair fields a previous version or a hand-edited state may be missing; the
	// private key is the truth and the public half is derived from it.
	record.PrivateKeyFile = keyRelative
	record.PublicKey = result.Pairs.PublicKey
	record.Fingerprint = result.Pairs.Fingerprint
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	if record.ApprovalStatus == "" {
		record.ApprovalStatus = APPROVAL_NOT_APPLIED
	}
	if record.StatusSource == "" {
		record.StatusSource = STATUS_SOURCE_GEN
	}
	if err := store.PutRepo(record); err != nil {
		return GenResult{}, err
	}
	if strings.TrimSpace(options.BaseURL) != "" {
		root, err := NormalizeBaseURL(options.BaseURL)
		if err != nil {
			return GenResult{}, err
		}
		if err := home.WriteConfig(root, now); err != nil {
			return GenResult{}, err
		}
		result.WroteConfig = true
	}
	if err := store.Save(); err != nil {
		return GenResult{}, err
	}
	result.Record = record
	return result, nil
}

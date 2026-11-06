// Package repokey holds the pieces of the repository-owned API key flow that are
// independent of storage and transport: the delivery envelope a caller decrypts, the
// public-key identity, and the repository identity a key belongs to.
//
// Nothing here reads a secret from disk or from a request body. The delivery
// envelope protects a key on its way to the caller's machine (PRD 4.4, AC-5): OMC
// encrypts to the caller's registered public key, the caller decrypts locally with
// its private key, and OMC never holds the private half.
package repokey

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Algorithm names the envelope construction. It is carried inside every envelope so
// a future algorithm can be introduced without invalidating stored ciphertext (PRD 4.4).
const Algorithm = "x25519-hkdf-sha256-aes256gcm"

const (
	// publicKeyPrefix marks the text form of a caller public key, so a value that is
	// not one is refused instead of being fed to the curve parser.
	publicKeyPrefix = "x25519:"
	// envelopeVersion is the schema version of Envelope.
	envelopeVersion = 1
	// hkdfInfo binds the derived key to this purpose; a different flow using the same
	// ECDH secret would derive a different key.
	hkdfInfo = "omc-repo-key-delivery-v1"
)

var (
	ErrInvalidPublicKey = errors.New("invalid_public_key")
	ErrInvalidEnvelope  = errors.New("invalid_envelope")
	ErrDecryptFailed    = errors.New("decrypt_failed")
)

// Envelope is the delivery ciphertext. The ephemeral public key lets the caller
// derive the same key with its private half; nonce is the AEAD nonce. All three are
// base64 (standard, padded), and the whole value is stored and returned as compact
// JSON.
type Envelope struct {
	Version            int    `json:"v"`
	Algorithm          string `json:"alg"`
	EphemeralPublicKey string `json:"epk"`
	Nonce              string `json:"nonce"`
	Ciphertext         string `json:"ciphertext"`
}

// ParsePublicKey reads the text form produced by a caller's key generation tool:
// "x25519:" followed by the 32-byte public key in standard base64. Whitespace is
// ignored so a key pasted from a shell or a YAML value still parses.
func ParsePublicKey(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, publicKeyPrefix) {
		return nil, ErrInvalidPublicKey
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(strings.TrimPrefix(value, publicKeyPrefix)))
	if err != nil || len(raw) != 32 {
		return nil, ErrInvalidPublicKey
	}
	// Validate that the point is on the curve by importing it; a random 32 bytes that
	// happens not to be a valid X25519 public key would otherwise be accepted and
	// produce an envelope nobody can open.
	if _, err := ecdh.X25519().NewPublicKey(raw); err != nil {
		return nil, ErrInvalidPublicKey
	}
	return raw, nil
}

// PublicKeyFingerprint is the stable, non-secret identity of a public key: the
// SHA-256 of its bytes, base64url-encoded with a short prefix. It is what the
// approval page shows and what a changed-key application is detected by (AC-34).
func PublicKeyFingerprint(raw []byte) string {
	digest := sha256.Sum256(raw)
	return publicKeyPrefix + "sha256:" + base64.RawURLEncoding.EncodeToString(digest[:])
}

// Encrypt seals plaintext to the caller's public key. The result is compact JSON
// suitable for a JSON field or a console clipboard.
func Encrypt(publicKey []byte, plaintext string) (string, error) {
	recipient, err := ecdh.X25519().NewPublicKey(publicKey)
	if err != nil {
		return "", ErrInvalidPublicKey
	}
	ephemeral, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", fmt.Errorf("generate ephemeral key: %w", err)
	}
	shared, err := ephemeral.ECDH(recipient)
	if err != nil {
		return "", fmt.Errorf("derive shared secret: %w", err)
	}
	key, err := hkdf.Key(sha256.New, shared, nil, hkdfInfo, 32)
	if err != nil {
		return "", fmt.Errorf("derive envelope key: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create envelope cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create envelope gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate envelope nonce: %w", err)
	}
	sealed := gcm.Seal(nil, nonce, []byte(plaintext), nil)
	envelope := Envelope{
		Version:            envelopeVersion,
		Algorithm:          Algorithm,
		EphemeralPublicKey: base64.StdEncoding.EncodeToString(ephemeral.PublicKey().Bytes()),
		Nonce:              base64.StdEncoding.EncodeToString(nonce),
		Ciphertext:         base64.StdEncoding.EncodeToString(sealed),
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return "", fmt.Errorf("encode envelope: %w", err)
	}
	return string(encoded), nil
}

// Decrypt opens an envelope with the caller's private key. It exists for this
// package's own tests and for a caller-side helper; OMC itself never calls it with a
// real key, because OMC never holds a private key (AC-35).
func Decrypt(privateKey []byte, envelopeJSON string) (string, error) {
	var envelope Envelope
	if err := json.Unmarshal([]byte(envelopeJSON), &envelope); err != nil {
		return "", ErrInvalidEnvelope
	}
	if envelope.Version != envelopeVersion || envelope.Algorithm != Algorithm {
		return "", ErrInvalidEnvelope
	}
	private, err := ecdh.X25519().NewPrivateKey(privateKey)
	if err != nil {
		return "", ErrInvalidEnvelope
	}
	ephemeralRaw, err := base64.StdEncoding.DecodeString(envelope.EphemeralPublicKey)
	if err != nil || len(ephemeralRaw) != 32 {
		return "", ErrInvalidEnvelope
	}
	ephemeral, err := ecdh.X25519().NewPublicKey(ephemeralRaw)
	if err != nil {
		return "", ErrInvalidEnvelope
	}
	nonce, err := base64.StdEncoding.DecodeString(envelope.Nonce)
	if err != nil {
		return "", ErrInvalidEnvelope
	}
	sealed, err := base64.StdEncoding.DecodeString(envelope.Ciphertext)
	if err != nil {
		return "", ErrInvalidEnvelope
	}
	shared, err := private.ECDH(ephemeral)
	if err != nil {
		return "", ErrInvalidEnvelope
	}
	key, err := hkdf.Key(sha256.New, shared, nil, hkdfInfo, 32)
	if err != nil {
		return "", ErrInvalidEnvelope
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", ErrInvalidEnvelope
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", ErrInvalidEnvelope
	}
	plaintext, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", ErrDecryptFailed
	}
	return string(plaintext), nil
}

// RandomToken returns a fresh one-time authorization token for an approval link.
func RandomToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// TokenFingerprint is the stored form of an authorization token. The token is high
// entropy, so a plain SHA-256 is enough to make the stored value useless to anyone
// who reads the database; the link itself is held by the caller.
func TokenFingerprint(token string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return "sha256:" + base64.RawURLEncoding.EncodeToString(digest[:])
}

// GenerateKeyValue returns a fresh gateway caller key. The prefix matches the value
// the console's "add key" dialog generates, so an operator cannot tell an issued key
// from a hand-created one by its shape.
func GenerateKeyValue() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate key: %w", err)
	}
	return "sk-cpa-" + hex.EncodeToString(raw), nil
}

// NormalizeRepo turns a self-reported repository into the identity a key is bound
// to. The preference order is the PRD's (4.1): a remote identity first, a local
// directory name when there is no remote.
//
// GitHub's remote reduces to "org/repo" because that is the spelling the product's
// own examples use. Keeping the host would make the same repository produce two
// identities depending on whether an agent pasted a clone URL or the short form.
func NormalizeRepo(value string) (repoID string, hasRemote bool, err error) {
	return normalizeRepo(value, true)
}

// RepoDisplay preserves the submitted spelling without exposing URL credentials.
func RepoDisplay(value string) (string, error) {
	display, _, err := normalizeRepo(value, false)
	return display, err
}

func normalizeRepo(value string, foldGitHub bool) (repoID string, hasRemote bool, err error) {
	original := strings.TrimSpace(value)
	if original == "" {
		return "", false, errors.New("repo_required")
	}
	if len(original) > 256 || strings.ContainsAny(original, "\r\n\t") {
		return "", false, errors.New("repo_invalid")
	}
	trimmed := strings.TrimSuffix(strings.TrimSuffix(original, "/"), ".git")

	// scp-style remote: git@github.com:org/repo
	if at := strings.Index(trimmed, "@"); at >= 0 && at+1 < len(trimmed) {
		if colon := strings.Index(trimmed[at+1:], ":"); colon >= 0 {
			host := trimmed[at+1 : at+1+colon]
			path := trimmed[at+1+colon+1:]
			if host != "" && path != "" && !strings.ContainsAny(host, " \\") {
				return repoIdentity(host, path, foldGitHub)
			}
		}
	}
	// URL form: https://host/org/repo, ssh://git@host/org/repo
	if strings.Contains(trimmed, "://") {
		withoutScheme := trimmed[strings.Index(trimmed, "://")+3:]
		hostEnd := strings.Index(withoutScheme, "/")
		if hostEnd > 0 {
			host := withoutScheme[:hostEnd]
			if at := strings.LastIndex(host, "@"); at >= 0 {
				host = host[at+1:]
			}
			path := withoutScheme[hostEnd+1:]
			if host != "" && path != "" {
				return repoIdentity(host, path, foldGitHub)
			}
		}
		return "", false, errors.New("repo_invalid")
	}
	// An absolute or drive-qualified path is a local directory, never a remote
	// shorthand: "E:/works/org/repo" is a checkout, not a host named "E:".
	isAbsolutePath := strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, "\\") ||
		strings.HasPrefix(trimmed, "./") || strings.HasPrefix(trimmed, "../") ||
		(len(trimmed) >= 2 && trimmed[1] == ':' && ((trimmed[0] >= 'A' && trimmed[0] <= 'Z') || (trimmed[0] >= 'a' && trimmed[0] <= 'z')))
	// org/repo shorthand.
	if !isAbsolutePath && strings.Contains(trimmed, "/") && !strings.Contains(trimmed, "\\") && !strings.Contains(trimmed, " ") {
		segments := strings.Split(trimmed, "/")
		nonEmpty := make([]string, 0, len(segments))
		for _, segment := range segments {
			if segment != "" {
				nonEmpty = append(nonEmpty, segment)
			}
		}
		if len(nonEmpty) >= 2 {
			return repoIdentity("", strings.Join(nonEmpty, "/"), foldGitHub)
		}
	}
	// A local directory: the last path segment, either separator.
	directory := original
	if index := strings.LastIndexAny(directory, "/\\"); index >= 0 {
		directory = directory[index+1:]
	}
	directory = strings.TrimSpace(directory)
	if directory == "" || directory == "." || directory == ".." {
		return "", false, errors.New("repo_invalid")
	}
	return directory, false, nil
}

func repoIdentity(host, path string, foldGitHub bool) (string, bool, error) {
	path = strings.Trim(path, "/")
	path = strings.TrimSuffix(path, ".git")
	path = strings.Trim(path, "/")
	if path == "" || strings.ContainsAny(path, " \t\r\n") {
		return "", false, errors.New("repo_invalid")
	}
	host = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(host)), "www.")
	if host == "" || host == "github.com" {
		owner := strings.SplitN(path, "/", 2)[0]
		isGitHubShorthand := strings.Count(path, "/") == 1 && !strings.ContainsAny(owner, ".:") && !strings.EqualFold(owner, "localhost")
		if foldGitHub && (host == "github.com" || isGitHubShorthand) {
			path = strings.ToLower(path)
		}
		return path, true, nil
	}
	return host + "/" + path, true, nil
}

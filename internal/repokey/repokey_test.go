package repokey

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
)

func newKeyPair(t *testing.T) (*ecdh.PrivateKey, []byte, string) {
	t.Helper()
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	public := private.PublicKey().Bytes()
	return private, public, publicKeyPrefix + base64.StdEncoding.EncodeToString(public)
}

// TestEnvelopeRoundTrip protects the delivery contract: only the private half of the
// registered public key opens the envelope, and the JSON carries everything the
// caller needs to derive the key.
func TestEnvelopeRoundTrip(t *testing.T) {
	private, public, text := newKeyPair(t)
	parsed, err := ParsePublicKey(text)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if string(parsed) != string(public) {
		t.Fatalf("parsed key differs from the encoded one")
	}

	envelope, err := Encrypt(parsed, "sk-cpa-0123456789abcdef")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	for _, want := range []string{`"v":1`, `"alg":"` + Algorithm + `"`, `"epk"`, `"nonce"`, `"ciphertext"`} {
		if !strings.Contains(envelope, want) {
			t.Fatalf("envelope %s does not describe itself with %s", envelope, want)
		}
	}
	plaintext, err := Decrypt(private.Bytes(), envelope)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if plaintext != "sk-cpa-0123456789abcdef" {
		t.Fatalf("plaintext = %q", plaintext)
	}

	// A different private key must not open it.
	other, _, _ := newKeyPair(t)
	if _, err := Decrypt(other.Bytes(), envelope); err == nil {
		t.Fatal("an unrelated private key opened the envelope")
	}
}

func TestEnvelopeTamperIsRefused(t *testing.T) {
	private, public, _ := newKeyPair(t)
	envelope, err := Encrypt(public, "secret")
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(envelope, `"v":1`, `"v":2`, 1)
	if _, err := Decrypt(private.Bytes(), tampered); err == nil {
		t.Fatal("an envelope with an unknown version must not open")
	}
}

func TestParsePublicKeyRejectsOtherForms(t *testing.T) {
	for _, value := range []string{
		"",
		"sk-cpa-0123",
		"x25519:not-base64",
		"x25519:" + base64.StdEncoding.EncodeToString([]byte("too-short")),
	} {
		if _, err := ParsePublicKey(value); err == nil {
			t.Fatalf("value %q must be refused", value)
		}
	}
}

func TestPublicKeyFingerprintIsStableAndDoesNotCarryTheKey(t *testing.T) {
	_, public, _ := newKeyPair(t)
	first := PublicKeyFingerprint(public)
	second := PublicKeyFingerprint(public)
	if first != second {
		t.Fatalf("fingerprint is not stable")
	}
	if strings.Contains(first, base64.StdEncoding.EncodeToString(public)) {
		t.Fatalf("fingerprint repeats the public key: %s", first)
	}
	_, other, _ := newKeyPair(t)
	if PublicKeyFingerprint(other) == first {
		t.Fatalf("different keys share a fingerprint")
	}
}

func TestNormalizeRepo(t *testing.T) {
	cases := []struct {
		input     string
		want      string
		hasRemote bool
	}{
		{"LambdaTheory/oh-my-cpa", "lambdatheory/oh-my-cpa", true},
		{"https://github.com/LambdaTheory/oh-my-cpa.git", "lambdatheory/oh-my-cpa", true},
		{"git@github.com:LambdaTheory/oh-my-cpa.git", "lambdatheory/oh-my-cpa", true},
		{"https://gitlab.com/team/project/", "gitlab.com/team/project", true},
		{"E:/works/my-local-project", "my-local-project", false},
		{"C:\\Users\\me\\scratch", "scratch", false},
		{"just-a-dir", "just-a-dir", false},
	}
	for _, testCase := range cases {
		got, hasRemote, err := NormalizeRepo(testCase.input)
		if err != nil {
			t.Fatalf("%q: %v", testCase.input, err)
		}
		if got != testCase.want || hasRemote != testCase.hasRemote {
			t.Fatalf("%q = (%q, %v), want (%q, %v)", testCase.input, got, hasRemote, testCase.want, testCase.hasRemote)
		}
	}
	for _, value := range []string{"", "   ", "https://", ".."} {
		if _, _, err := NormalizeRepo(value); err == nil {
			t.Fatalf("%q must be refused", value)
		}
	}
}

func TestGenerateKeyValueShape(t *testing.T) {
	value, err := GenerateKeyValue()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(value, "sk-cpa-") || len(value) != len("sk-cpa-")+32 {
		t.Fatalf("unexpected key shape %q", value)
	}
	again, _ := GenerateKeyValue()
	if value == again {
		t.Fatal("two generated keys are identical")
	}
}

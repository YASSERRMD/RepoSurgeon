package secrets

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func testKey(t *testing.T) string {
	t.Helper()
	raw := bytes.Repeat([]byte{0x11}, keySize)
	return base64.StdEncoding.EncodeToString(raw)
}

func testEncryptor(t *testing.T, version int) *Encryptor {
	t.Helper()
	encryptor, err := New(testKey(t), version)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return encryptor
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	encryptor := testEncryptor(t, 3)
	if encryptor.KeyVersion() != 3 {
		t.Fatalf("KeyVersion = %d, want 3", encryptor.KeyVersion())
	}
	plaintext := []byte("sf-live-abcdef1234567890")
	ciphertext, nonce, err := encryptor.EncryptSecret(plaintext)
	if err != nil {
		t.Fatalf("EncryptSecret: %v", err)
	}
	if len(nonce) != nonceSize {
		t.Fatalf("nonce length = %d, want %d", len(nonce), nonceSize)
	}
	if bytes.Contains(ciphertext, plaintext) {
		t.Fatal("ciphertext contains the plaintext")
	}
	recovered, err := encryptor.DecryptSecret(ciphertext, nonce)
	if err != nil {
		t.Fatalf("DecryptSecret: %v", err)
	}
	if !bytes.Equal(recovered, plaintext) {
		t.Fatalf("recovered = %q, want %q", recovered, plaintext)
	}
}

func TestEncryptionIsRandomised(t *testing.T) {
	encryptor := testEncryptor(t, 1)
	plaintext := []byte("same-value-every-time")
	first, firstNonce, err := encryptor.EncryptSecret(plaintext)
	if err != nil {
		t.Fatalf("EncryptSecret: %v", err)
	}
	second, secondNonce, err := encryptor.EncryptSecret(plaintext)
	if err != nil {
		t.Fatalf("EncryptSecret: %v", err)
	}
	if bytes.Equal(first, second) && bytes.Equal(firstNonce, secondNonce) {
		t.Fatal("re-encrypting the same value produced identical ciphertext")
	}
}

func TestDecryptRejectsWrongKey(t *testing.T) {
	encryptor := testEncryptor(t, 1)
	ciphertext, nonce, err := encryptor.EncryptSecret([]byte("rotate-me-please"))
	if err != nil {
		t.Fatalf("EncryptSecret: %v", err)
	}
	otherKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x22}, keySize))
	other, err := New(otherKey, 2)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := other.DecryptSecret(ciphertext, nonce); err == nil {
		t.Fatal("decrypting with the wrong key must fail")
	}
}

func TestDecryptRejectsTamperedCiphertext(t *testing.T) {
	encryptor := testEncryptor(t, 1)
	ciphertext, nonce, err := encryptor.EncryptSecret([]byte("tamper-evident"))
	if err != nil {
		t.Fatalf("EncryptSecret: %v", err)
	}
	tampered := append([]byte(nil), ciphertext...)
	tampered[len(tampered)-1] ^= 0xff
	if _, err := encryptor.DecryptSecret(tampered, nonce); err == nil {
		t.Fatal("tampered ciphertext must fail authentication")
	}
}

func TestDecryptRejectsTruncatedEnvelope(t *testing.T) {
	encryptor := testEncryptor(t, 1)
	if _, err := encryptor.DecryptSecret([]byte("short"), make([]byte, nonceSize)); err == nil {
		t.Fatal("truncated envelope must fail")
	}
	if _, err := encryptor.DecryptSecret(bytes.Repeat([]byte{0}, 128), []byte("short")); err == nil {
		t.Fatal("wrong nonce length must fail")
	}
}

func TestNewRejectsBadKeys(t *testing.T) {
	cases := map[string]string{
		"empty":       "",
		"not base64":  "not-base64!!",
		"too short":   base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x01}, 16)),
		"whitespace ": "   ",
	}
	for name, key := range cases {
		if _, err := New(key, 1); err == nil {
			t.Fatalf("%s key must be rejected", name)
		}
	}
}

func TestKeyVersionDefaultsToOne(t *testing.T) {
	encryptor, err := New(testKey(t), 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if encryptor.KeyVersion() != 1 {
		t.Fatalf("KeyVersion = %d, want 1", encryptor.KeyVersion())
	}
}

func TestFingerprintStableAndDistinct(t *testing.T) {
	first := Fingerprint("value-one")
	if first != Fingerprint("value-one") {
		t.Fatal("fingerprint is not stable")
	}
	if first == Fingerprint("value-two") {
		t.Fatal("different values share a fingerprint")
	}
	if len(first) != 16 || strings.ContainsAny(first, "ghijklmnop") {
		t.Fatalf("fingerprint %q must be 16 lowercase hex characters", first)
	}
}

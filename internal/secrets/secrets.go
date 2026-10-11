// Package secrets encrypts provider credentials with envelope AEAD encryption.
//
// Layout per credential (stored in credential_metadata.ciphertext):
//
//	nonce_wrap (12) || wrapped_dek (32+16) || value_ciphertext (n+16)
//
// A fresh data encryption key (DEK) is generated per credential and wrapped
// with the key encryption key (KEK) supplied by the deployment. The nonce
// column holds the value nonce. Rotating the KEK only changes what new rows
// are wrapped with; existing rows stay readable while the old key is still
// available, and are re-wrapped on next rotation.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Size constants for AES-256-GCM.
const (
	keySize   = 32
	nonceSize = 12
	tagSize   = 16
)

// ErrInvalidKeyFormat is returned when the configured KEK is not 32 base64
// bytes.
var ErrInvalidKeyFormat = errors.New("secrets: key encryption key must be 32 bytes, base64 encoded")

// Encryptor wraps the deployment key and performs envelope encryption.
type Encryptor struct {
	kek        []byte
	keyVersion int
}

// New builds an encryptor from a base64-encoded 32-byte key and its version.
func New(base64Key string, version int) (*Encryptor, error) {
	trimmed := strings.TrimSpace(base64Key)
	if trimmed == "" {
		return nil, errors.New("secrets: key encryption key is required; set RS_CREDENTIAL_KEK")
	}
	raw, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil {
		return nil, ErrInvalidKeyFormat
	}
	if len(raw) != keySize {
		return nil, ErrInvalidKeyFormat
	}
	if version <= 0 {
		version = 1
	}
	return &Encryptor{kek: raw, keyVersion: version}, nil
}

// KeyVersion reports the configured key version.
func (e *Encryptor) KeyVersion() int { return e.keyVersion }

// EncryptSecret seals plaintext and returns the envelope together with the
// value nonce.
func (e *Encryptor) EncryptSecret(plaintext []byte) (ciphertext, nonce []byte, err error) {
	dek := make([]byte, keySize)
	if _, err := rand.Read(dek); err != nil {
		return nil, nil, fmt.Errorf("secrets: generate data key: %w", err)
	}
	wrapped, wrapNonce, err := e.seal(e.kek, dek)
	if err != nil {
		return nil, nil, err
	}
	valueNonce := make([]byte, nonceSize)
	if _, err := rand.Read(valueNonce); err != nil {
		return nil, nil, fmt.Errorf("secrets: generate value nonce: %w", err)
	}
	sealed := e.sealWithNonce(dek, valueNonce, plaintext)

	envelope := make([]byte, 0, nonceSize+len(wrapped)+len(sealed))
	envelope = append(envelope, wrapNonce...)
	envelope = append(envelope, wrapped...)
	envelope = append(envelope, sealed...)
	return envelope, valueNonce, nil
}

// DecryptSecret opens an envelope produced by EncryptSecret.
func (e *Encryptor) DecryptSecret(ciphertext, nonce []byte) ([]byte, error) {
	if len(ciphertext) < nonceSize+keySize+tagSize {
		return nil, errors.New("secrets: ciphertext envelope is truncated")
	}
	if len(nonce) != nonceSize {
		return nil, errors.New("secrets: value nonce has the wrong length")
	}
	wrapNonce := ciphertext[:nonceSize]
	wrappedDEK := ciphertext[nonceSize : nonceSize+keySize+tagSize]
	sealed := ciphertext[nonceSize+keySize+tagSize:]

	dek, err := e.open(e.kek, wrapNonce, wrappedDEK)
	if err != nil {
		return nil, fmt.Errorf("secrets: unwrap data key (wrong RS_CREDENTIAL_KEK or key version?): %w", err)
	}
	plaintext, err := e.open(dek, nonce, sealed)
	if err != nil {
		return nil, fmt.Errorf("secrets: open value ciphertext: %w", err)
	}
	return plaintext, nil
}

// seal encrypts plaintext under key with a fresh nonce and returns both.
func (e *Encryptor) seal(key, plaintext []byte) (ciphertext, nonce []byte, err error) {
	nonce = make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("secrets: generate nonce: %w", err)
	}
	return e.sealWithNonce(key, nonce, plaintext), nonce, nil
}

// sealWithNonce encrypts plaintext under key using the caller's nonce.
func (e *Encryptor) sealWithNonce(key, nonce, plaintext []byte) []byte {
	gcm, err := newGCM(key)
	if err != nil {
		return nil
	}
	return gcm.Seal(nil, nonce, plaintext, nil)
}

// open decrypts ciphertext under key and nonce.
func (e *Encryptor) open(key, nonce, ciphertext []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, nonce, ciphertext, nil)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secrets: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secrets: new gcm: %w", err)
	}
	return gcm, nil
}

// Fingerprint returns a stable, non-reversible fingerprint for a secret value.
// It is used to detect duplicate credentials without storing or comparing the
// secret itself, and is safe to expose in API responses.
func Fingerprint(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:8])
}

// Masked renders a human-safe placeholder. The plaintext never leaves the
// encryptor, so callers only ever show this.
const Masked = "********"

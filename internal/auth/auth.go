// Package auth implements local authentication: password hashing, opaque
// sessions with rotation and revocation, refresh tokens, CSRF tokens and
// brute-force controls. It has no HTTP dependency so it can be exercised in
// isolation.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// HashPassword hashes a password with argon2id using the parameters below and
// returns the encoded PHC string.
//
// HashPassword hashes a password with argon2id using the parameters below and
// returns the encoded PHC string.
//
// Argon2id parameters. These follow the OWASP recommendation for a
// memory-hard hash at 19 MiB / 2 iterations / 1 thread.
const (
	argonTime    = 2
	argonMemory  = 19 * 1024
	argonThreads = 1
	argonKeyLen  = 32
	saltLen      = 16
)

// ErrPasswordTooShort is returned for passwords shorter than the policy.
var ErrPasswordTooShort = errors.New("auth: password must be at least 12 characters")

// MinPasswordLength is the documented policy.
const MinPasswordLength = 12

// HashPassword hashes a password with argon2id and returns the encoded string.
func HashPassword(password string) (string, error) {
	if len(password) < MinPasswordLength {
		return "", ErrPasswordTooShort
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: read salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen) // #nosec G115
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword reports whether password matches the encoded hash. The
// comparison is constant time.
func VerifyPassword(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || !strings.HasPrefix(parts[1], "argon2id") {
		return false, errors.New("auth: password hash has an unexpected format")
	}
	var memory, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return false, fmt.Errorf("auth: parse hash parameters: %w", err)
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, errors.New("auth: decode salt")
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, errors.New("auth: decode key")
	}
	key := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(expected))) // #nosec G115
	if subtle.ConstantTimeCompare(key, expected) == 1 {
		return true, nil
	}
	return false, nil
}

// TokenBytes is the entropy of session tokens.
const TokenBytes = 32

// NewToken returns a URL-safe opaque session token and its storage hash. Only
// the hash is persisted, so a database leak cannot be replayed against the
// API.
func NewToken() (token, tokenHash string, err error) {
	raw := make([]byte, TokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("auth: read token entropy: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), HashToken(raw), nil
}

// NewCSRFToken returns a CSRF token and its hash.
func NewCSRFToken() (token, tokenHash string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("auth: read csrf entropy: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, hex.EncodeToString(HashRaw(raw)), nil
}

// HashToken hashes a decoded token for storage.
func HashToken(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// HashRaw returns the hex sha256 of raw.
func HashRaw(raw []byte) []byte {
	sum := sha256.Sum256(raw)
	return sum[:]
}

// HashCSRFToken hashes a CSRF token for comparison.
func HashCSRFToken(token string) string {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return ""
	}
	return hex.EncodeToString(HashRaw(raw))
}

// ConstantTimeEqual compares two hashes without leaking timing information.
func ConstantTimeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

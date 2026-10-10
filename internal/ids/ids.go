// Package ids issues cryptographically strong, opaque identifiers.
//
// Identifiers are intentionally unguessable and encode their entity type as a
// prefix so that logs and APIs stay readable without leaking sequence
// information across tenants.
package ids

import (
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"strings"
)

// Alphabet is the canonical, lowercase, unpadded Crockford-compatible base32
// alphabet used for all identifiers.
const Alphabet = "abcdefghijklmnopqrstuvwxyz234567"

// entropyBytes is the number of random bytes per identifier (160 bits).
const entropyBytes = 20

// New returns a new opaque identifier with the given entity prefix, formatted
// as "<prefix>_<26 chars>". The prefix must be non-empty and contain only
// lowercase letters, digits and underscores.
func New(prefix string) (string, error) {
	if err := validatePrefix(prefix); err != nil {
		return "", err
	}
	var buf [entropyBytes]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("ids: read random bytes: %w", err)
	}
	enc := base32.NewEncoding(Alphabet).WithPadding(base32.NoPadding)
	return prefix + "_" + enc.EncodeToString(buf[:]), nil
}

// MustNew is New but panics on failure. It is intended for callers that cannot
// propagate an error (for example table-driven test fixtures).
func MustNew(prefix string) string {
	id, err := New(prefix)
	if err != nil {
		panic(err)
	}
	return id
}

func validatePrefix(prefix string) error {
	if prefix == "" {
		return fmt.Errorf("ids: prefix must not be empty")
	}
	for _, r := range prefix {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '_':
		default:
			return fmt.Errorf("ids: prefix %q contains invalid character %q", prefix, r)
		}
	}
	return nil
}

// Valid reports whether id has the shape produced by New for the given prefix.
func Valid(id, prefix string) bool {
	if !strings.HasPrefix(id, prefix+"_") {
		return false
	}
	body := strings.TrimPrefix(id, prefix+"_")
	if len(body) != 32 {
		return false
	}
	for _, r := range body {
		if !strings.ContainsRune(Alphabet, r) {
			return false
		}
	}
	return true
}

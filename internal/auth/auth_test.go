package auth

import (
	"strings"
	"testing"
)

func TestHashAndVerifyPassword(t *testing.T) {
	password := "correct-horse-battery-staple"
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("hash = %q, want argon2id prefix", hash)
	}
	ok, err := VerifyPassword(hash, password)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !ok {
		t.Fatal("correct password was rejected")
	}
	ok, err = VerifyPassword(hash, "wrong-password-entirely")
	if err != nil {
		t.Fatalf("VerifyPassword (wrong): %v", err)
	}
	if ok {
		t.Fatal("wrong password was accepted")
	}
}

func TestHashPasswordPolicy(t *testing.T) {
	if _, err := HashPassword("short"); err == nil {
		t.Fatal("short password must be rejected")
	}
}

func TestHashPasswordSalts(t *testing.T) {
	first, err := HashPassword("another-long-password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	second, err := HashPassword("another-long-password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if first == second {
		t.Fatal("identical hashes indicate a fixed salt")
	}
	ok, err := VerifyPassword(second, "another-long-password")
	if err != nil || !ok {
		t.Fatalf("second hash must verify: ok=%v err=%v", ok, err)
	}
}

func TestVerifyPasswordRejectsMalformedHash(t *testing.T) {
	for _, encoded := range []string{"", "plain", "$argon2id$v=19$garbage$aa$bb", "$argon2id$v=19$m=1,t=1,p=1$notbase64$bb"} {
		if _, err := VerifyPassword(encoded, "some-long-password"); err == nil {
			t.Fatalf("VerifyPassword(%q) succeeded, want error", encoded)
		}
	}
}

func TestNewTokenIsUniqueAndHashed(t *testing.T) {
	seenTokens := map[string]bool{}
	seenHashes := map[string]bool{}
	for i := 0; i < 200; i++ {
		token, hash, err := NewToken()
		if err != nil {
			t.Fatalf("NewToken: %v", err)
		}
		if len(token) < 40 {
			t.Fatalf("token %q is too short", token)
		}
		if seenTokens[token] || seenHashes[hash] {
			t.Fatal("tokens or hashes repeated")
		}
		seenTokens[token] = true
		seenHashes[hash] = true
		if raw, err := decodeBase64URL(token); err == nil {
			if got := HashToken(raw); got != hash {
				t.Fatal("hash does not match the decoded token")
			}
		} else {
			t.Fatalf("token is not base64url: %v", err)
		}
	}
}

func TestCSRFTokenHashing(t *testing.T) {
	token, hash, err := NewCSRFToken()
	if err != nil {
		t.Fatalf("NewCSRFToken: %v", err)
	}
	if HashCSRFToken(token) != hash {
		t.Fatal("HashCSRFToken does not match the issued token")
	}
	if HashCSRFToken("not-base64!!") != "" {
		t.Fatal("invalid tokens must hash to an empty string")
	}
	if !ConstantTimeEqual(hash, hash) {
		t.Fatal("equal hashes must compare equal")
	}
	if ConstantTimeEqual(hash, "") || ConstantTimeEqual("", hash) {
		t.Fatal("different hashes must not compare equal")
	}
}

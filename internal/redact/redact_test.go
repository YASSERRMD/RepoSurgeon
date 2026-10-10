package redact

import (
	"strings"
	"testing"
)

func TestStringRedactsKeyValuePairs(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"password pair", "user=admin password=hunter2", "user=admin password=<redacted>"},
		{"token pair", "token: abc123def456", "token: <redacted>"},
		{"api key pair", "api_key=sk-abcdef1234567890", "api_key=<redacted>"},
		{"authorization header", "Authorization: Bearer eyJhbGciOiJIUzI1NiJ9", "Authorization=<redacted>"},
		{"database url with password", "connect to postgres://admin:s3cret@db.example.com:5432/app", "connect to postgres://<redacted>@db.example.com:5432/app"},
		{"jwt", "id eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.c2lnbmF0dXJl is bad", "id <redacted> is bad"},
		{"github token", "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ012345", "<redacted>"},
		{"plain text untouched", "SELECT 1 FROM users WHERE id = 42", "SELECT 1 FROM users WHERE id = 42"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := String(tc.input)
			if got != tc.want {
				t.Fatalf("String(%q) = %q, want %q", tc.input, got, tc.want)
			}
			if strings.Contains(got, "hunter2") || strings.Contains(got, "s3cret") {
				t.Fatalf("secret leaked in %q", got)
			}
		})
	}
}

func TestHeader(t *testing.T) {
	if got := Header("Authorization", "Bearer abc"); got != "<redacted>" {
		t.Fatalf("Authorization header = %q", got)
	}
	if got := Header("Content-Type", "application/json"); got != "application/json" {
		t.Fatalf("Content-Type header = %q", got)
	}
}

func TestMapRedactsSensitiveKeysRecursively(t *testing.T) {
	in := map[string]any{
		"outer": map[string]any{"token": "abc123", "keep": "value"},
		"list":  []any{"password=secret", "plain"},
	}
	got := Map(in)
	outer := got["outer"].(map[string]any)
	if outer["token"] != "<redacted>" {
		t.Fatalf("sensitive key not redacted: %v", outer)
	}
	if outer["keep"] != "value" {
		t.Fatalf("non-secret was altered: %v", outer)
	}
	list := got["list"].([]any)
	if list[0] != "password=<redacted>" {
		t.Fatalf("list secret not redacted: %v", list)
	}
	// Input must not be mutated.
	if in["outer"].(map[string]any)["token"] != "abc123" {
		t.Fatal("input map was mutated")
	}
}

func TestMapRedactsStringMap(t *testing.T) {
	got := Map(map[string]any{
		"headers": map[string]string{"Authorization": "Bearer abcdef123456", "Accept": "application/json"},
	})
	headers := got["headers"].(map[string]string)
	if headers["Authorization"] != "<redacted>" {
		t.Fatalf("authorization header not redacted: %v", headers)
	}
	if headers["Accept"] != "application/json" {
		t.Fatalf("accept header altered: %v", headers)
	}
}

func TestKeysListsSensitiveNames(t *testing.T) {
	keys := Keys()
	if len(keys) < 5 {
		t.Fatalf("expected sensitive key list, got %v", keys)
	}
	found := false
	for _, k := range keys {
		if k == "database_url" {
			found = true
		}
	}
	if !found {
		t.Fatalf("database_url missing from %v", keys)
	}
}

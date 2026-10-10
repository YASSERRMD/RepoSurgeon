package ids

import (
	"strings"
	"testing"
)

func TestNewShape(t *testing.T) {
	id, err := New("run")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !strings.HasPrefix(id, "run_") {
		t.Fatalf("id %q missing prefix", id)
	}
	if len(id) != len("run_")+32 {
		t.Fatalf("id %q has length %d, want %d", id, len(id), len("run_")+32)
	}
	if !Valid(id, "run") {
		t.Fatalf("Valid rejected fresh id %q", id)
	}
}

func TestNewUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 5000; i++ {
		id, err := New("op")
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if seen[id] {
			t.Fatalf("duplicate id %q after %d draws", id, i)
		}
		seen[id] = true
	}
}

func TestNewRejectsBadPrefix(t *testing.T) {
	for _, prefix := range []string{"", "Run", "run-id", "run id"} {
		if _, err := New(prefix); err == nil {
			t.Fatalf("New(%q) succeeded, want error", prefix)
		}
	}
}

func TestValidRejectsWrongPrefixAndCharset(t *testing.T) {
	if Valid("task_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1", "run") {
		t.Fatal("accepted wrong prefix")
	}
	if Valid("run_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1", "run") {
		t.Fatal("accepted invalid charset character")
	}
	if Valid("run_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "run") {
		t.Fatal("accepted wrong length")
	}
}

func TestMustNewPanicsOnBadPrefix(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	MustNew("Bad-Prefix")
}

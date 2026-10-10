#!/usr/bin/env bash
# Regenerate fixtures/sample-repo deterministically. Used by evaluation
# scenarios that need a known-broken repository (M17) and by worktree tests
# (M05).
set -euo pipefail

repo_root="$(git -C "$(dirname "$0")" rev-parse --show-toplevel 2>/dev/null || cd "$(dirname "$0")/.." && pwd)"
fixture="$repo_root/fixtures/sample-repo"

rm -rf "$fixture"
mkdir -p "$fixture"

cat >"$fixture/go.mod" <<'EOF'
module example.com/sample

go 1.25
EOF

cat >"$fixture/main.go" <<'EOF'
package main

import "fmt"

// Add returns a + b. This fixture intentionally contains an off-by-one defect
// so evaluation scenarios can observe real diagnosis and repair.
func Add(a, b int) int {
	return a + b + 1
}

func main() {
	fmt.Println(Add(2, 2))
}
EOF

cat >"$fixture/main_test.go" <<'EOF'
package main

import "testing"

func TestAdd(t *testing.T) {
	cases := []struct {
		a, b, want int
	}{
		{2, 2, 4},
		{-1, 1, 0},
		{40, 2, 42},
	}
	for _, tc := range cases {
		if got := Add(tc.a, tc.b); got != tc.want {
			t.Errorf("Add(%d, %d) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
EOF

cat >"$fixture/README.md" <<'EOF'
# fixtures/sample-repo

Deterministic fixture with a seeded defect: `Add` returns `a + b + 1`, so
`go test ./...` fails on three cases. Used by M05 worktree tests, M07 tool
execution tests and the M17 evaluation laboratory.
EOF

echo "regenerated $fixture"

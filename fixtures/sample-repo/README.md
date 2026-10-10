# fixtures/sample-repo

A tiny, deterministic Git repository used by tests that need a real checkout
(worktree management in M05, tool execution in M07, evaluation scenarios from
M17). It intentionally contains a failing test so fixtures can exercise
failure recovery.

Contents:

- `go.mod` — module `example.com/sample` (no external dependencies)
- `main.go` — a pure function `Add` with a subtle off-by-one bug
- `main_test.go` — hidden-style test asserting correct behaviour (fails)
- `README.md` — describes the seeded defect

Regenerate deterministically with `scripts/make-fixture-repo.sh`.

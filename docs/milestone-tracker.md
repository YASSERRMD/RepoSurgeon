# Milestone tracker

Live status of the M00–M19 plan. The authoritative specification for each
phase is `docs/specification/MILESTONES/`; durable execution history lives in
`docs/PHASE_STATUS.md`.

| Phase | Capability | Branch | Status | PR | Merge SHA |
|---|---|---|---|---|---|
| M00 | Repository foundation and engineering governance | merged, branch deleted | merged | #1 | `d69e78b` |
| M01 | Domain model, persistence, project isolation | merged, branch deleted | merged | #2 | recorded in M02 report |
| M01 | Domain model, persistence, project isolation | — | not started | — | — |
| M02 | Authentication, authorization, secure credentials | merged, branch deleted | merged | #3 | recorded in M03 report |
| M03 | Step 5 Preview model gateway and tool calling | merged, branch deleted | merged | #4 | recorded in M04 report |
| M04 | GitHub integration, repository registration, issue sync | — | not started | — | — |
| M05 | Safe repository checkout, branching, worktree management | merged, branch deleted | merged | #6 | recorded in M06 report |
| M06 | Durable agent run state machine and worker scheduling | merged, branch deleted | merged | #7 | recorded in M07 report |
| M07 | Isolated tool execution and human approval gateway | merged, branch deleted | merged | #8 | recorded in M08 report |
| M08 | Repository discovery, code search, long-context builder | merged, branch deleted | merged | #9 | recorded in M09 report |
| M09 | Issue investigation, plans, acceptance criteria | `milestone/m09-...` | in progress | — | — |
| M10 | Atomic code change and patch engine | — | not started | — | — |
| M11 | Test discovery, execution, evidence collection | — | not started | — | — |
| M12 | Iterative debugging, recovery, state reconciliation | — | not started | — | — |
| M13 | Git commits, pull requests, CI review, safe merge | — | not started | — | — |
| M14 | Complete web workbench and developer experience | — | not started | — | — |
| M15 | Realtime timeline, auditability, cost, observability | — | not started | — | — |
| M16 | Multimodal screenshot understanding and frontend verification | — | not started | — | — |
| M17 | Benchmark laboratory and model comparison | — | not started | — | — |
| M18 | Security red team, compliance, reliability hardening | — | not started | — | — |
| M19 | Release, deployment, full journey verification, handover | — | not started | — | — |

Rules: mark a phase complete only after branch merge is verified on `main`.
Unavailable external prerequisites are recorded as `BLOCKED` with exact
remediation, never as passed.

# RepoSurgeon — Stable API and Agent Protocol Contracts

## REST conventions

- Prefix `/api/v1`; all stateful calls authenticated, authorized and org/project-scoped; consistent `application/json`.
- Client `Idempotency-Key` required for create/run/PR operations; `X-Request-ID` echo for traceability.
- Cursor pagination: `{items:[...], next_cursor:"..."}`; stable sort; explicit `403/404` semantics preventing cross-org leakage.
- Errors: `{"error":{"code":"permission_denied","message":"...","request_id":"...","details":{}}}` with redaction.
- Schema changes update `api/openapi.yaml` and generated frontend types atomically, plus contract tests.

## Core routes by phase

```
GET /health/live
GET /health/ready
GET /api/v1/system/info
POST /api/v1/auth/login
POST /api/v1/auth/logout
GET /api/v1/me
GET|POST /api/v1/organizations
GET|POST /api/v1/projects
GET|PATCH /api/v1/projects/{project_id}
POST /api/v1/model-connections/{connection_id}/test
GET /api/v1/github/repos
POST /api/v1/issues/import
POST /api/v1/webhooks/github
GET /api/v1/workspaces/{workspace_id}/tree
GET /api/v1/workspaces/{workspace_id}/files
GET /api/v1/workspaces/{workspace_id}/search
POST /api/v1/runs
GET /api/v1/runs/{run_id}
POST /api/v1/runs/{run_id}/start
POST /api/v1/runs/{run_id}/cancel
GET /api/v1/runs/{run_id}/plan
POST /api/v1/runs/{run_id}/plan/approve
GET /api/v1/runs/{run_id}/diff
POST /api/v1/runs/{run_id}/checks
GET /api/v1/runs/{run_id}/checks
POST /api/v1/runs/{run_id}/pr
POST /api/v1/runs/{run_id}/merge
GET /api/v1/runs/{run_id}/events/stream
GET /api/v1/runs/{run_id}/export
POST /api/v1/evaluations
GET /api/v1/evaluations/{evaluation_id}/results
```

Exact request/response DTOs evolve contract-first in relevant phase; API list here is the baseline, not an excuse to return static dummy responses.

## Example `POST /api/v1/runs`

```json
{
  "project_id": "opaque-project-id",
  "issue_id": "opaque-issue-id",
  "model": {"provider": "stepfun", "id": "step-5-preview", "reasoning_effort": "high"},
  "limits": {"max_steps": 60, "max_duration_seconds": 3600, "max_usd": 10.0},
  "policy": {"external_pr_allowed": true, "merge_requires_approval": true}
}
```

Return 201 plus `{ "id": "...", "state": "queued", "base_sha": "..." }` only after durable transaction; repeated idempotency key returns same ID. `project_id` and `issue_id` authorization are verified.

## Durable run states

`queued → intaking → analyzing → planning → [awaiting_approval] → editing → testing → reviewing → pr_ready → pr_open → completed`. Alternative states `failed`, `cancelled`, `blocked` can be reached under defined conditions. Recovery can go back to `analyzing` or `editing` with increasing attempt count, never remove historic events. Invalid transitions return `409 state_conflict`.

## Standard tool schema envelope

```json
{
  "tool": "apply_patch",
  "operation_id": "opaque-unique-id",
  "workspace_id": "opaque-workspace-id",
  "arguments": {
    "base_commit": "expected-git-sha",
    "path": "internal/example/service.go",
    "expected_sha256": "expected-file-hash",
    "unified_diff": "@@ -1,1 +1,1 @@ ..."
  }
}
```

Registry validates tool name, JSON schema, policy grants, path, effect ID and state before execution. `result` is a typed `{status, exit_code?, artifact_id?, diagnostics?, duration_ms}`; mutation receipts are persisted and deduplicated. Tools must **not** return unauthenticated user-supplied `success` as verified outcome.

## StepFun API integration

```
POST https://api.stepfun.ai/v1/chat/completions
Authorization: Bearer $STEP_API_KEY
Content-Type: application/json
```

```json
{
  "model": "step-5-preview",
  "messages": [{"role":"user","content":"Investigate this failing test using only the supplied tools."}],
  "reasoning_effort": "medium",
  "max_tokens": 2048,
  "tools": [{"type":"function","function":{"name":"read_file","description":"Read an authorized repository file","parameters":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}}}]
}
```

This is a **schema example**, not a live API invocation. Tool calls must be executed by RepoSurgeon, then tool results appended to messages with matching provider `tool_call_id`. Support streaming fragmented tool args and `usage.prompt_tokens`, `usage.cached_tokens` (if supplied), `usage.completion_tokens`. `reasoning_effort` may be `low`, `medium`, or `high`.

## Event stream

```
id: 123
event: run.step_completed
data: {"run_id":"...","seq":123,"step_id":"...","kind":"tool","status":"succeeded","occurred_at":"2026-10-10T10:00:00Z"}
```

Event IDs replay from `Last-Event-ID`/cursor. No raw tokens, access keys or model hidden reasoning. Do not derive source of truth from the event transport; always persist first.

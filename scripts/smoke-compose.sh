#!/usr/bin/env bash
# Compose smoke: build images, start the stack, prove the application is
# genuinely reachable, break the database to prove readiness flips, then tear
# down. This is the M00 mandatory verification scenario and is reused by CI.
#
# Requires: docker (with compose plugin), curl.
set -euo pipefail

compose=(docker compose -p reposurgeon-smoke)
failures=0

log() { printf '\n=== %s ===\n' "$*"; }
check() {
  local label="$1" expected="$2" actual="$3"
  if [ "$expected" = "$actual" ]; then
    echo "PASS: $label ($actual)"
  else
    echo "FAIL: $label expected $expected got $actual"
    failures=$((failures + 1))
  fi
}

cleanup() {
  log "tearing down"
  "${compose[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT

log "build images"
"${compose[@]}" build

log "start stack"
"${compose[@]}" up -d

log "wait for api liveness"
live=""
for _ in $(seq 1 60); do
  live="$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 http://127.0.0.1:8081/health/live || true)"
  [ "$live" = "200" ] && break
  sleep 2
done
check "GET /health/live" "200" "$live"

ready="$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 http://127.0.0.1:8081/health/ready || true)"
check "GET /health/ready (healthy)" "200" "$ready"

info="$(curl -s --max-time 10 http://127.0.0.1:8081/api/v1/system/info || true)"
echo "system info: $info"
case "$info" in
  *'"name":"reposurgeon"'*) echo "PASS: system info payload" ;;
  *) echo "FAIL: system info payload"; failures=$((failures + 1)) ;;
esac

web="$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 http://127.0.0.1:8080/ || true)"
check "web index" "200" "$web"

proxied="$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 http://127.0.0.1:8080/api/v1/system/info || true)"
check "web proxies api" "200" "$proxied"

runner_health="$(curl -s --max-time 10 http://127.0.0.1:8091/v1/healthz || true)"
echo "runner healthz: $runner_health"
case "$runner_health" in
  *'"status":"ok"'*) echo "PASS: runner healthz" ;;
  *) echo "FAIL: runner healthz"; failures=$((failures + 1)) ;;
esac

if [ -n "${RS_RUNNER_TOKEN:-}" ]; then
  exec_result="$(curl -s --max-time 20 -X POST http://127.0.0.1:8091/v1/exec \
    -H "Authorization: Bearer ${RS_RUNNER_TOKEN}" \
    -H 'Content-Type: application/json' \
    -d '{"operation_id":"smoke-echo","program":"/bin/echo","args":["smoke"],"cwd":"."}' || true)"
  echo "runner exec: $exec_result"
  case "$exec_result" in
    *'"status":"succeeded"'*) echo "PASS: runner exec" ;;
    *) echo "FAIL: runner exec"; failures=$((failures + 1)) ;;
  esac
fi

log "break PostgreSQL and prove readiness flips to 503"
"${compose[@]}" stop postgres >/dev/null

unavailable=""
for _ in $(seq 1 30); do
  unavailable="$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 http://127.0.0.1:8081/health/ready || true)"
  [ "$unavailable" = "503" ] && break
  sleep 2
done
check "GET /health/ready (postgres down)" "503" "$unavailable"

body="$(curl -s --max-time 10 http://127.0.0.1:8081/health/ready || true)"
echo "unavailable report: $body"
case "$body" in
  *'"status":"unavailable"'*) echo "PASS: readiness reports unavailable with detail" ;;
  *) echo "FAIL: readiness report shape"; failures=$((failures + 1)) ;;
esac

live_after="$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 http://127.0.0.1:8081/health/live || true)"
check "GET /health/live during outage" "200" "$live_after"

log "restore PostgreSQL and prove readiness recovers"
"${compose[@]}" start postgres >/dev/null

recovered=""
for _ in $(seq 1 60); do
  recovered="$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 http://127.0.0.1:8081/health/ready || true)"
  [ "$recovered" = "200" ] && break
  sleep 2
done
check "GET /health/ready (recovered)" "200" "$recovered"

if [ "$failures" -gt 0 ]; then
  log "SMOKE FAILED with $failures failing check(s)"
  exit 1
fi
log "SMOKE PASSED"

#!/usr/bin/env bash
# Verify source-commit identity and absence of AI/co-author trailers.
#
# Usage:
#   scripts/ci/verify-commit-metadata.sh <base-ref> <head-ref>
# Defaults to origin/main when invoked in CI on a pull request.
set -euo pipefail

base="${1:-origin/main}"
head="${2:-HEAD}"

if ! git rev-parse --verify "$base" >/dev/null 2>&1; then
  echo "verify-commit-metadata: base ref '$base' not found; skipping" >&2
  exit 0
fi

expected_name="yasserrmd"
expected_email="arafath.yasser@gmail.com"

# GitHub creates the merge commit itself; only source commits are verified.
mapfile -t commits < <(git rev-list --no-merges "$base".."$head")

if [ "${#commits[@]}" -eq 0 ]; then
  echo "verify-commit-metadata: no source commits in range"
  exit 0
fi

status=0
for sha in "${commits[@]}"; do
  author_name="$(git show -s --format='%an' "$sha")"
  author_email="$(git show -s --format='%ae' "$sha")"
  committer_name="$(git show -s --format='%cn' "$sha")"
  committer_email="$(git show -s --format='%ce' "$sha")"
  body="$(git show -s --format='%B' "$sha")"
  short="$(git log -1 --format='%h %s' "$sha")"

  if [ "$author_name" != "$expected_name" ] || [ "$author_email" != "$expected_email" ]; then
    echo "FAIL: author identity '$author_name <$author_email>' on $short" >&2
    status=1
  fi
  if [ "$committer_name" != "$expected_name" ] || [ "$committer_email" != "$expected_email" ]; then
    echo "FAIL: committer identity '$committer_name <$committer_email>' on $short" >&2
    status=1
  fi
  if printf '%s' "$body" | grep -Eiq '^(Co-authored-by|Generated-by|Assisted-by|Signed-off-by|AI-authored-by):'; then
    echo "FAIL: forbidden trailer in commit message on $short" >&2
    status=1
  fi
done

if [ "$status" -eq 0 ]; then
  echo "verify-commit-metadata: OK (${#commits[@]} source commits use yasserrmd <arafath.yasser@gmail.com> with no AI trailers)"
fi
exit "$status"

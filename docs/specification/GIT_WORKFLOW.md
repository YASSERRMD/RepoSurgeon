# Git / PR Workflow — Mandatory for Claude Code Implementing RepoSurgeon

## 1. Identity and integrity

```bash
git config --local user.name "yasserrmd"
git config --local user.email "arafath.yasser@gmail.com"
export GIT_AUTHOR_NAME="yasserrmd"
export GIT_AUTHOR_EMAIL="arafath.yasser@gmail.com"
export GIT_COMMITTER_NAME="yasserrmd"
export GIT_COMMITTER_EMAIL="arafath.yasser@gmail.com"
```

Before every commit inspect `git status`, `git diff --check`, scoped changed files and staged content. No `Co-authored-by`, AI or bot attribution/trailer in new commits. Use `git log --format='%h %an <%ae> / %cn <%ce>%n%B' origin/main..HEAD` and check. Never rewrite shared commits to change identity. Do not expose `.env`, GitHub tokens, StepFun keys, or secrets in commit history.

## 2. Lifecycle for each M00–M19

```bash
git fetch origin main
git switch main
git pull --ff-only origin main
git switch -c milestone/m00-repository-foundation-and-engineering-governance
# Work in coherent small changes, run tests for each.
git add <explicit-paths>
git diff --cached --check
git commit -m "m00: implement functional health endpoints"
# Repeat with several specific atomic commits.
# Run full mandatory quality pipeline and inspect secret/author violations.
git push -u origin HEAD
gh pr create --base main --head milestone/m00-repository-foundation-and-engineering-governance --title "M00: Repository foundation" --body-file docs/milestone-reports/M00.md
# Inspect actual GitHub check status; fix on branch if red.
gh pr checks <PR_NUMBER> --watch
# Merge only when authorized, approved, all checks pass, repo policy permits merge commits:
gh pr merge <PR_NUMBER> --merge --delete-branch
# Verify actual merged state, then sync:
gh pr view <PR_NUMBER> --json state,mergedAt,mergeCommit,url
git switch main
git pull --ff-only origin main
# Check local branch deletion and remove only after verified merge.
```

**This is an illustrative M00 command sequence, not proof any command has run.** Exact branch slugs are listed in milestone files. `gh` must be authorized as an actual permitted GitHub account; configuring Git author metadata does not grant repository permissions or change GitHub PR actor.

## 3. Stop conditions

If branch protections, required reviews, failed CI, lack of permission, missing remote, unavailable `gh`, or authentication block a PR/merge: report and store exact **BLOCKED** state; do not push to main, use `--admin`, force merge, bypass checks or invent success. Fix correctable CI failures, but never disable failing gates just to merge. It is acceptable to continue independent safe coding if dependencies allow; do not claim a blocked phase was complete.

## 4. Preserving evidence under PR-only rules

The PR itself must contain the milestone completion report draft and test evidence before merge, with PR URL added in subsequent branch commit or PR comment if necessary. After merge, the next milestone branch records previous final merge SHA. After M19, put final merge evidence in GitHub release and/or PR comment instead of breaking no-direct-main rule.

## 5. GitHub-generated merge metadata

GitHub may create merge commits with GitHub-managed committer identity. The exact `yasserrmd` identity requirement is enforced for all local/source commits Claude Code creates; verify and disclose unavoidable GitHub-managed merge metadata rather than forging platform identities. Never add any AI co-author metadata.

## 6. Branch deletion

Do not delete open/unmerged PR branches. `--delete-branch` is safe only after confirmed merge. If GitHub did not delete remote, first verify merged, then `git push origin --delete <milestone-branch>`; delete local with `git branch -d` only after merged. Never use `-D` to paper over unmerged work.

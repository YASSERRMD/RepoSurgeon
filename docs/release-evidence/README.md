# Release evidence

Every milestone produces immutable evidence in `docs/milestone-reports/MXX.md`
using the template at `docs/specification/TEMPLATES/PHASE_COMPLETION_REPORT.md`.

What each report must contain (from the specification):

- Phase ID and status (PASS / FAILED / BLOCKED / IN REVIEW / MERGED)
- Branch name and base commit SHA
- PR URL and number, actual CI run URLs and conclusions
- Merge SHA once verified on `main`
- New source commit hashes with author/committer verification
- Exact commands executed with outcomes (never invented)
- Security checks and the specific gates that apply to the phase
- Remaining risks and blockers with remediation

Evidence files are versioned in the milestone branch **before** the PR is
opened, so the review sees the proof rather than a promise. Screenshots and
Playwright artifacts live in `docs/milestone-reports/artifacts/MXX/`.

After M19 the final merge evidence is recorded in a GitHub release and/or PR
comment, because direct commits to `main` are forbidden.

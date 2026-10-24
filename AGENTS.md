# MCPJungle fork working agreements

This is the Jones-Systems fork of `mcpjungle/MCPJungle`. Publish fork work only
to `Jones-Systems/MCPJungle`; never push to `upstream` or open upstream issues,
pull requests, or comments without separate owner authorization.

## Tests

Right-size only Jones-added files and Jones-added cases within upstream files.
Preserve upstream-owned tests for clean synchronization. Diagnose failing tests
before changing them; never delete, skip, retry, or weaken them just to pass.
Keep tests protecting distinct behavior, especially authorization, durable
retirement, cleanup, and shipped regressions. Remove low-value tests separately
from behavior changes, with reasons and the coverage that remains.

## Checks and CI budget

At the October 24, 2026 audit baseline (`ffa61c9`), main has no GitHub Actions
workflows. Required automated checks: **none**. Advisory automated checks:
**none**. A check belonging only to an unmerged PR is not a gate for another PR.
The workflow proposed in PR #5 is pending and is not installed on main.

For Go behavior changes, build the dashboard assets needed by
`internal/dashboardui/embed.go`, then run the affected Go tests and the normal
`go test ./...` check. Use isolated, cleanup-owned build and test state. For
documentation-only changes, verify the diff, paths, and factual claims; a full
application build is not required.

When adding CI, update this declaration in the same PR with exact required and
advisory check names. Fast PR checks are required unless explicitly declared
otherwise. Native, nightly, label-triggered, end-to-end, packaging, and release
checks are advisory unless explicitly promoted. Advisory failures need a fix or
tracked follow-up and do not hold a merge. Do not change branch protection or
rulesets as part of CI maintenance.

Target push-to-last-required-check median <= 10 minutes and p90 <= 20 minutes,
including queue time; each required job or shard must finish within 6m30s.
Use PR concurrency cancellation, avoid duplicate work, and gate macOS PR/push
jobs to the platform paths they prove. Place slow or native qualification on
nightly, manual, label, or relevant path triggers, preserving a suitable trigger
for every test. Measure runner minutes and queue time separately.

The audit and pending workflow follow-up are recorded in
[the right-sizing work note](owner-plans/test-right-sizing-20261024.md).

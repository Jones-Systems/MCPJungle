# MCPJungle test right-sizing and CI efficiency

<!-- codex-section:begin id="worknote.mcpjungle-test-right-sizing#ctx.artifact-header.001" -->
Artifact Type: `work-note`

Artifact ID: `worknote.mcpjungle-test-right-sizing`

Purpose: Record the owned-test audit, CI baseline, decisions, and follow-up.

Governing artifact: `none`

Continuity owner: Jones Code thread `01a16c63-8dd8-76d0-986d-e9dcc9cfd1cd`

Consumers: fork contributors and CI maintainers

Authority effect: none
<!-- codex-section:end id="worknote.mcpjungle-test-right-sizing#ctx.artifact-header.001" -->

## Scope and authority

M Jones approved this General-lane workstream on October 24, 2026: audit only
Jones-added tests, improve owned CI where present, declare check policy, perform
one final bulk review, and publish and merge this lane's PRs. No authority covers
another workstream's PR, upstream publication, host installation, sudo, service
changes, secrets, branch deletion, or ruleset changes.

The coordinator is the sole Git owner and writer of the supplied linked
worktree on `work/right-size-tests-20261024`. Baseline main is
`ffa61c9f44cbf3e93b20b1cf9786173e4d92e58a`. Ownership is the diff from the
upstream/main merge base `12648be5edc70391dc3f9c4aecfd37b83b3e30be`.
No upstream-owned tests are candidates for removal. Graphify reported no
selected graph for this repository; direct source and Git history supplied
ownership evidence.

Rules were read from Codex-V3 origin/main at
`86230e4b70b4ec914606f349f015335bee153c4d`, including the right-size-tests skill,
test-value practice, CI budget, native-tier cap, and shared General-lane plan.
Explicit Frontier Max routing requested Astra/max initial planning; the root
retains its owner-selected Sol/high coordination pin. The installed preference
CLI rejects the newer `frontier_max` name; no preference store was changed.

## Baseline and decisions

There are **87 Go test files and 443 top-level Test functions**. Jones added
**11 files with 44 top-level Test functions**, including three subprocess
fixture entry points; 41 are behavioral test entry points. Subtests and ordinary
loops are not counted as top-level functions. Jones also added six explicit-host
subcases inside `cmd/start_test.go` and changed its default-host expectation.
Changes to `internal/service/mcp/util_test.go` synchronize existing log fixtures
without adding test cases.

Decision: **keep all**. Counts remain 87/443 overall and 11/44 in added files.
No removals, merges, skips, retries, assertion weakening, or production changes.
No missing behavior was established that warrants adding tests during this pass.

| Owned test or group | Decision | Reason and coverage retained |
| --- | --- | --- |
| `cmd/registration_guard_test.go` (2) | Keep | Inode aliases, writer exclusion/reacquisition, and unsupported durability modes protect single-writer ownership. |
| `cmd/start_test.go` additions | Keep | Safe default and explicit IPv4/IPv6/interface overrides protect the binding contract. One flag case overlaps an upstream precedence case; restructuring the shared table to save one cheap case is not worthwhile. |
| `internal/db/registration_storage_test.go` (1) | Keep | Verifies effective SQLite durability configuration rather than a declared constant. |
| `registration_definition_test.go` (1) | Keep | Normalization, duplicate keys, malformed input, ordering, and exact definition binding are distinct boundary cases. |
| `registration_lifecycle_test.go` (2, one fixture) | Keep | Failed initialization must close its real child; the fixture supplies the process boundary. |
| `registration_managed_test.go` (21) | Keep | Covers terminal fencing, cancellation/deadlines, transaction rollback, readiness, restart metadata, authorization filters, cleanup failure/retry, and conflicting definitions. |
| `registration_inventory_test.go` (2) | Keep | Distinguishes inactive capability placeholders from active, malformed, paginated, or failed inventories before tools are accepted. |
| `registration_inventory_process_linux_test.go` (2, one fixture) | Keep | Real-process rejection and drain prove cleanup/publication behavior that in-memory transport cases cannot. |
| `registration_restart_pending_test.go` (2) | Keep | Persisted cleanup blockers survive restart without blocking unrelated names; status remains bounded, cancellable, and read-only. |
| `registration_status_writer_test.go` (1) | Keep | A coordinated server-status writer cannot resurrect a deleted managed registration. |
| `legacy_catalog_lifecycle_test.go` (4) | Keep | Single/bulk prompt and resource writers have separate paths; retirement races and legacy compatibility need their own assertions. |
| `internal/api/registration_contract_test.go` (6, one fixture) | Keep | HTTP capabilities, strict rejection, terminal receipts, disconnect cancellation, and pending-status response protect the external API boundary. |
| `internal/service/mcp/util_test.go` fixture changes | Keep | Synchronization preserves existing upstream assertions under concurrent log writes; it adds no harness-only test suite. |

Cheap signals found no owned long-skipped tests, obsolete feature tests,
framework-only tests, or test-helper test suites. Lifecycle unit, HTTP, restart,
and real-child tests overlap in subject but establish different boundaries.
No seeded defect was needed because no coverage was removed.

## CI baseline and collision

Baseline main has no workflow files. Main PR CI runner-minutes and push-to-green
distributions are **unavailable**, not zero. There is no current required job to
shard, relocate, or optimize, and no native-app E2E tier to cap.

Open [PR #5](https://github.com/Jones-Systems/MCPJungle/pull/5) proposes the sole
workflow, `.github/workflows/owner-tests.yml`, at head
`0f6f3fc9ee32d37560aa97142304a4586d7e1968`. It also edits the guard and API tests.
This is a collision, not authority to update, push, close, or merge that PR.
This lane changes only this note and root `AGENTS.md`.

The four available historical runs are all PR #5 runs: three failed, followed
by one success. They do not establish a representative latency distribution or
a test-flake rate. The latest successful
[run 39552142248](https://github.com/Jones-Systems/MCPJungle/actions/runs/39552142248)
provides this limited comparison:

| Measure | Observation |
| --- | --- |
| Workflow creation to last check completion | 3m39s; exact push time unavailable |
| Initial queue/start delay from workflow creation | 2s |
| Linux job | 3m28s |
| macOS job | 3m37s |
| Sum of job wall time | 7m05s (7.08 runner-minutes, unrounded, not billing-rounded) |
| macOS job wall time | 3m37s (3.62 runner-minutes) |
| Median / p90 push-to-green | Unavailable; only one successful run and no push timestamp |

These job times fit 6m30s, but do not qualify main or prove a weekly budget.
The run was created October 21, 2026 at 1:53:37 AM EDT, and its last check
completed at 1:57:16 AM EDT.

## Pending workflow follow-up

PR #5's workflow runs the full suite on both Linux and macOS for every PR and
main push, and has no PR concurrency cancellation. Before that workflow becomes
main CI, its owning work should gate macOS PR/push events to Darwin-specific
surfaces or move native qualification to a suitable scheduled/manual trigger;
add `concurrency` with `cancel-in-progress: true`; separate fast required Linux
checks from advisory native/end-to-end qualification; and declare exact check
names. Every retained test must still have a suitable trigger. The 20-minute job
timeout and 12-minute test timeout are safeguards, not evidence of budget fit.

This follow-up remains with the existing PR's owner. No other workstream thread
was contacted. A GitHub issue could not be created because issues are disabled
in `Jones-Systems/MCPJungle`; this note is the durable fallback. Do not enable
issues or alter repository settings for this audit.

## Verification and bulk review

The local baseline passed on the unchanged source: **35 top-level entries and
73 subtests, zero failures or skips**. This includes two fixture entry points.
The command used the already-present Go 1.27.1 toolchain, `GOMAXPROCS=2`, and:

```text
go test -json -mod=readonly -count=1 -p=1 -parallel=1 -timeout=5m \
  -run '^(TestRegistration|TestManaged|TestLegacyCatalog|TestRestart)' \
  ./internal/service/mcp ./internal/db
```

Package test time was 11.239s for MCP service and 0.020s for storage. Total wall
time including cold compilation was 145.621s. The slowest selected top-level
test was real-process inactive-inventory cleanup at 2.26s, followed by legacy
catalog compatibility at 1.99s. One passing run establishes no flake rate.
The filter excludes `TestLegacyDeleteDrainsItsWorkerWithoutPermanentRetirement`;
that test and the eight cmd/API entry points were source-reviewed but not run
locally. The clean checkout lacks the dashboard bundle needed to compile the
cmd/API packages. No full-suite or supported-version qualification is claimed;
the historical PR #5 run uses different source and Go 1.26.5.

Fresh admission allowed one serial test command: 37.19% CPU used, 33.51 GiB RAM
available, one fully used core 6.25% of capacity. Scratch used one unique root
under coordinator-owned disk-backed task evidence, with 4 GiB estimated peak
and 10 GiB residual reserve against 450,996,826,112 available bytes. Cleanup
removed that exact root after success; the small JSONL result remains as durable
local evidence. No host tools or services were installed or reconfigured.

The Astra/max initial planner independently agreed that all tests should stay.
Final sensitive-area Astra/xhigh bulk review is pending. No production or test
source changes are planned, so repeated application builds after documentation
edits are not required.

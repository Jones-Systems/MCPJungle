# Owner fork ledger

The owner fork is `Jones-Systems/MCPJungle`; upstream is
`mcpjungle/MCPJungle`. The incorporated upstream first-parent baseline is
`12648be5edc70391dc3f9c4aecfd37b83b3e30be`. Owner lifecycle work was
incorporated by `ed9c6c61a2445ed2605d290243f6e1198bc37a97` (PR #1).
The Mac continuation starts from owner main
`ffa61c9f44cbf3e93b20b1cf9786173e4d92e58a` and preserves PR #2 startup
quarantine, PR #3 loopback defaults, and PR #4 hardened quickstart.

This ledger records the Mac continuation's seams. It is not a complete
classification of every inherited owner change. Reconstructed Mac working
files are porting evidence; unavailable historical repair commits are not
claimed as recovered Git ancestry.

| Feature | Ownership and seams | Verification | Disposition |
| --- | --- | --- | --- |
| Darwin registration guard | Owner-added `cmd/registration_guard_*` and `internal/service/mcp/registration_platform*`; platform-specific lock implementations | Native Darwin contention, alias, descriptor-close and writer-lifetime tests; Linux guard tests | kept |
| Cancellation and terminal cleanup | Inline owner lifecycle patches in `internal/service/mcp/registration_lifecycle.go`; no independent upstream lifecycle extension point exists | `registration_session_cleanup_test.go` and existing registration contract tests | kept |
| Transport command construction | Narrow hook in upstream-owned `internal/service/mcp/util.go`; preserves request-bound and stateful lifetimes | `registration_owned_wait_test.go` observes the SDK-owned wait without becoming another waiter | kept |
| Integrated EOF qualification | Owner-added `qualification/` fixtures and `qualification` build-tag observation bridge; no observer in the normal binary | `internal/api/registration_owned_qualification_test.go` exercises real handlers, service, SDK transport and Python adapter before gateway shutdown | kept |
| Platform regression CI | Owner-added `.github/workflows/owner-tests.yml` | Serial full Go tests on Linux and macOS, with isolated temporary/module/build state | kept |

The upstream module path and product name remain unchanged. No public release,
tag, update feed, or signing identity is created by this change. A local build
should carry an owner version through
`-X github.com/mcpjungle/mcpjungle/pkg/version.Version=OWNER_VERSION` and retain
its exact source commit and binary hash in the local installation receipt.

The Python stdio adapter belongs to `Jones-Systems/Codex-V3`, under
`tools/mcpjungle`. Install the qualified gateway and adapter revisions as a
pair. Their process and journal identity support must match the target OS;
Linux `/proc` assumptions are not portable to macOS. Registration contract 1
and capability checks remain the compatibility boundary. No database or
journal schema migration is introduced for Darwin support.

The CI qualification pair pins the adapter to Codex-V3 commit
`a1e875f001f374ab800863401a2af42eb71566b6`. An adapter revision change
requires new paired qualification; a gateway-only unit test does not replace
it. The ordinary Go suite excludes the observation bridge. CI separately runs
the `qualification`-tag API test with explicit adapter, Python and receipt paths.

Mac qualification must distinguish transport-owned backend waits,
frontend-owned waits, terminal registration state, and journal retirement.
PID disappearance and later recovery alone do not prove graceful EOF cleanup.
An unresolved wait, identity mismatch, or cleanup result must remain fenced
and retain the evidence required for recovery.

For future upstream imports, take upstream code first and reapply only the
recorded owner seams. Preserve both platform implementations and the newer
startup quarantine and loopback behavior. Run actual Linux and Darwin tests;
cross-compilation does not establish lock or process behavior.

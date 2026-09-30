# Engineering Spec — Gateway registration robustness projection

<!-- codex-section:begin id="spec.mcpjungle-gateway-robustness#ctx.artifact-header.001" -->
Artifact Type: `engineering-spec`

Artifact ID: `spec.mcpjungle-gateway-robustness`

Purpose: Bound interrupted MCP registration and preserve safe autonomous recovery.

Governing artifact: `spec.mcpjungle-robustness`

Specification owner: coordinator

Integration owner: coordinator

Consumers: gateway, adapter, diagnostic and planner builders; qualification and activation operators

Authority effect: none
<!-- codex-section:end id="spec.mcpjungle-gateway-robustness#ctx.artifact-header.001" -->

<!-- codex-section:begin id="spec.mcpjungle-gateway-robustness#ctx.summary.001" -->
## Project summary

Interrupted MCP registration can outlive its caller. The current shared fence is necessary but blocks unrelated new registrations. Provide terminal name retirement, bounded safe recovery and explicit planner MCP startup while preserving ordinary compatibility.
<!-- codex-section:end id="spec.mcpjungle-gateway-robustness#ctx.summary.001" -->

<!-- codex-section:begin id="spec.mcpjungle-gateway-robustness#ctx.research.001" -->
## Research and decision basis

Official 0.4.6 source and installed archive provenance, independent upstream and adapter evidence, Opus 5.5/high initial framing and independent GPT-6.1-Sol/xhigh plan were compared by Sol/xhigh. Adopt name-based tombstones instead of new UUID/journal schema; reject detached discovery, uncertain adoption and lock release on legacy gateways. The lifecycle API, proxy readiness, process launch gate and shared migration are load-bearing compatibility boundaries. Required direct fixtures are mapped below. Native planning evidence and exact source bindings live in worknote.mcpjungle-robustness; planning is not operational success.
<!-- codex-section:end id="spec.mcpjungle-gateway-robustness#ctx.research.001" -->

<!-- codex-section:begin id="spec.mcpjungle-gateway-robustness#req.robustness.001" -->
Durably retire each exact managed name; prevent later publication/launch; bound startup and recovery; preserve legacy safety, protocol fidelity, private data boundaries and explicit MCP selection.
<!-- codex-section:end id="spec.mcpjungle-gateway-robustness#req.robustness.001" -->

<!-- codex-section:begin id="spec.mcpjungle-gateway-robustness#iface.gateway-contract.001" -->
## Gateway registration contract

Contract 1 uses the existing unique registration name for one creation lifetime. Names are never reused after retirement. Journals remain v2. Capability is opt-in through `gateway_registration_contract: 1` and a validated gateway response.

Absent or 0 retains legacy behavior. Explicit 1 requires valid advertised contract 1; missing, invalid, unavailable or downgraded capability fails before POST without fallback. Preserve old uncertainty and clean only this attempt's proved pre-submit ownership.

One gateway-owned create slot covers both legacy and managed discovery, client closure, DB/proxy publication and final worker outcome. Waiting consumes the whole registration deadline and is cancellable; a cancelled waiter cannot launch later. Resolve and unrelated cleanup never acquire the slot. Capability 1 requires it. Mixed versions serialize gateway creation while HTTP/readbacks may overlap; old client-lock behavior is not guaranteed. Test concurrent old/new maximum active creation one, cancelled waiter no launch and unrelated confirmed cleanup during negotiated POST.

`GET /api/v0/capabilities` returns `{"registration_contract":1,"registration_deadline_seconds":45,"registration_resolve_deadline_seconds":50}` only when durable lifecycle storage, readiness/publication guards and the lifetime single-writer guard work. Otherwise return `{"registration_contract":0}`. Use the existing management authorization boundary.

Initially enable only SQLite/Linux. Acquire one lifetime advisory lock for the exact database before serving. Hold until registration workers drain; failed drain must not release while workers remain alive. Activation separately proves the old binary stopped, since it does not participate in this lock.

`POST /api/v0/servers?registration_contract=1` accepts the existing definition and returns the existing `201 {"server": ...}` only after discovery, temporary-client closure, required-row transaction and usable catalog publication. Accept stdio/tools-only, reject force. Reserve the name durably before launching. Duplicate active names return 409 registration_inflight; fenced names 409 name_fenced; different definitions 409 definition_mismatch. Unmarked legacy requests preserve their interface and best-effort optional capability semantics, but gain full discovery deadlines and unsuccessful-client closure.

Normalize typed definitions: exact valid name, stdio transport, nonempty absolute command, args omitted/null to ordered string array [], env omitted/null to string map {}, session_mode omitted/empty to stateless or explicit stateful/stateless, description omitted to empty. Reject duplicate JSON keys, unsupported fields and invalid types. Exact strings/array order matter, map order does not; do not normalize paths/Unicode or equate Go JSON with the adapter's local SHA256.

`POST /api/v0/servers/{name}/resolve` body is `{"registration": <exact definition>}`. Validate normalized definition, install durable fence, cancel/drain the name's worker, serialize with publication, establish frozen visible state. Unknown absent names must acquire durable tombstones before returning. Success is `200 {"registration_contract":1,"name":<name>,"outcome":"present"|"absent","terminal":true}`. It guarantees no later publication for that name. Present permits exact cleanup, never adoption. Timeout, cleanup failure, conflicting state or persistence failure gives non-200 and no terminal receipt. Repeating resolve safely reconciles a lost response.

Managed DELETE preserves existing interface, permanently fences, drains discovery/publication and completes exact cleanup before success. Tombstones never expire or disappear through DELETE. A legacy create cannot bypass an existing tombstone. Adapter uncertainty resolution must validate any present definition with fresh GET, delete, then obtain fresh absence. GET absence alone never proves termination.

One additive lifecycle table keyed by name binds normalized definition, managed row identity, minimal creation/readiness/fenced state. Discover into memory, transactionally commit all required entities, publish in batches behind readiness guards. Conversion/insertion failures abort managed publication. Before listening on restart fence unfinished work and remove/reconcile incomplete managed rows, reconstruct ready catalogs from rows without rediscovery, exclude incomplete tools from listing/invocation. Request context, whole 45-second lifecycle deadline, resolve and shutdown cancellation all apply; never detach discovery to Background. Preserve HTTP/OAuth/session interfaces. No general session-manager queue redesign.

Persist complete SDK-supported managed tool JSON in one additive tool-definition column so metadata/output schemas/titles survive ready publication and restart. Preserve established conversion for legacy rows. Qualify additive migration/downgrade on disposable databases; shared migration remains separately approved.
<!-- codex-section:end id="spec.mcpjungle-gateway-robustness#iface.gateway-contract.001" -->

<!-- codex-section:begin id="spec.mcpjungle-gateway-robustness#dec.source.001" -->
## Adapter, diagnostics and planner decisions

Classify all records read-only before recovery mutation. One validated server-list snapshot serves dead-intent checks. Reclaim independently safe intent/confirmed records even behind an uncertain record. Preserve exact definition/path/PID-generation checks, fresh post-delete readback and root-wide legacy quarantine. Limit elapsed work and counts; pass remaining time into HTTP and cleanup rather than allowing an operation to exceed the stated budget. Deferred safe cleanup remains retained, not an admission fence.

One cross-process create slot paces negotiated creates. Acquire it before short management sections, release management lock during POST/readback, reacquire for journal transitions. Cleanup never waits for create slot. Legacy and mixed-client paths retain existing serialization. Optional startup_budget_seconds is absolute from construction, covers all initialization, and refuses pre-submit when create/readback reserve cannot fit. Keep 60-second create and 20-second admission ceilings and existing error-category substrings. Approximately 85 seconds under 90-second client timeout is a qualification target, not a current configuration effect.

Management send uses cancellation guard; mark attempted transmission before underlying send. Only no attempted send proves pre-submit cancellation; every possibly transmitted POST retains uncertainty. Constructor partial failures roll back exact newly owned paths. Failed initialize sends its error then closes/retires promptly. A settled uncertain create requires reconnect, never automatic resubmission. Later explicit registration after confirmed retirement uses a fresh random name and clean owned paths.

New launcher/adapter pair use marker_dir/.launch.lock and a durable retirement sentinel. Launcher takes the gate, validates directory identity/sentinel after acquiring, prepares scratch, publishes/fsyncs marker, holds through exec with CLOEXEC. Retirement takes same gate, writes/fsyncs sentinel, stops only verified owned generations, verifies exit, then removes owned paths. A delayed launcher on an unlinked gate revalidates directory identity and refuses execution. Old-launcher uncertainty retains old clearance boundary. Bind paired immutable hashes in release manifest.

registration_state.py classifier accepts exact record/temp roots plus optional excluded current name, returns bounded entries with name/phase/owner_liveness/classification/validation_issue and internal validated definitions. It does no HTTP, locking, signals, writes or cleanup. status.py defaults to metadata-only; --gateway permits bounded GETs. Report reachability, admission blocked/open/unknown, counts and bounded blocking names; tool availability stays unprobed unless directly proven. No raw args/env/cwd or credentials in default output.

claude-plan always supplies --strict-mcp-config. Empty allowlist supplies explicit empty definitions. Nonempty allowlist requires explicit operator-supplied definitions with exactly allowed names, or fail before native invocation. Preserve --allowedTools, provider/session/fork behavior and named shadcn compatibility. Never discover/copy definitions from auth/config stores.
<!-- codex-section:end id="spec.mcpjungle-gateway-robustness#dec.source.001" -->

<!-- codex-section:begin id="spec.mcpjungle-gateway-robustness#ac.source.001" -->
## Acceptance and verification

Preserve all 32 baseline adapter tests and full browser/state/EOF behavior. Cheap unchanged before/after oracles: constructor rollback, one initial list GET for many intents, safe cleanup beyond uncertainty, pre-send cancellation, failed-initialize exit, strict planner exact selection. New contracts use direct isolated fixtures where old API absence is not a meaningful defect oracle.

Gateway fixture initializes then blocks tools/list on release events. Prove full deadline/disconnect close; resolve/delete vs publish; resolve-before-create starts no backend; lost resolve retry; mismatch no mutation; partial insert rollback; restart at reservation/commit/publication/ack; single writer; no terminal success with cleanup pending. Preserve legacy HTTP/OAuth/session/auth semantics. Adapter tests prove negotiated lock release and pacing, absolute budget, attempted-send uncertainty, legacy absent-then-late and partial-visible quarantine, malformed capability/downgrade, fresh-name lifetime, abrupt death and delayed pre-marker launcher fencing. Diagnostics prove tree digest unchanged, zero writes/locks/signals, GET-only traffic and blocked/open/unknown.

Root runs fresh-admitted one serial test process, with task-owned disk scratch and exact fixture cleanup on success/failure/cancellation. Codex union: mcpjungle-adapter, profiles-package, repository-integrity. External: affected packages, focused races, full native suite. Isolated old/candidate disposable gateways qualify retirement/readiness/rollback and three-service startup under client budget; bursts report overload honestly. Historical cause remains unknown, without blocking source fixes. Neither source acceptance nor tests authorize live activation.
<!-- codex-section:end id="spec.mcpjungle-gateway-robustness#ac.source.001" -->

<!-- codex-section:begin id="spec.mcpjungle-gateway-robustness#ctx.operator-interaction-inventory.001" -->
## Operator Interaction Inventory

| Effect | Owner action | Mechanics, readback and stop boundary |
| --- | --- | --- |
| Source/bootstrap tooling and isolated qualification | none | Root-owned branches, pinned local Go and disposable fixtures; no sudo/live state; preserve exact provenance. |
| Normal negotiated create/resolve/cleanup after activation | none | Typed contract and adapter owned-generation gates; error leaves quarantine; no password/TTY/owner-entered commands. |
| Read-only diagnostics | none within authorized metadata scope | status CLI metadata/GET only, bounded complete/partial results. |
| Existing old-launcher uncertain recovery and shared upgrade | One exact new activation envelope | Named service, old generation stop barrier, consistent DB backup, paired release, additive migration, scoped client configuration/reloads, interruption limit, exact current record ownership/readback, measured rollback. Source request does not approve these live effects. |
| Rollback | Covered by exact activation envelope | Qualify disposable upgraded DB downgrade; retire/drain managed names before old binary; additive table alone is not downgrade proof. Stop on lost rollback/unknown effects. |
| Credentials/authentication | separate exact authority if needed | No enrollment/rotation needed or authorized here; no protected-store copies. |
| Destructive/forced effects or exceptional fallback | separate exact target approval | No bulk cleanup, arbitrary kills, sudo, force push, publication/merge/deploy implied; stop on mismatch or unsupported old generations. |

Acceptance closes all required source units together. Shared live activation is the later consequential phase; old two-hour recovery approval is expired.
<!-- codex-section:end id="spec.mcpjungle-gateway-robustness#ctx.operator-interaction-inventory.001" -->

<!-- codex-section:begin id="spec.mcpjungle-gateway-robustness#ctx.ownership.001" -->
## Exact change surface and ownership

Root is sole Git owner. Builders are not alone and may edit only their machine task writer cone; no Git, heavy tests, live access or descendants. Root owns test starts, catalog/docs/build evidence and fan-in. External gateway tasks are projected in their own repository, never represented as Codex-local Go paths. Codex integration additionally requires root acceptance of the exact external gateway result. Classifier and adapter share one builder; accepted classifier seam releases status while adapter continues. Sequential fallback follows DAG; runtime launches use completion-order refill.
<!-- codex-section:end id="spec.mcpjungle-gateway-robustness#ctx.ownership.001" -->

<!-- codex-section:begin id="spec.mcpjungle-gateway-robustness#plan.implementation.001" -->
```json
{
  "schema_version": 1,
  "default_context_refs": [
    "spec.mcpjungle-gateway-robustness#ctx.summary.001",
    "spec.mcpjungle-gateway-robustness#iface.gateway-contract.001",
    "spec.mcpjungle-gateway-robustness#dec.source.001"
  ],
  "deferred_findings_sink_ref": null,
  "artifact_path_hints": {
    "spec.mcpjungle-gateway-robustness": "owner-plans/registration-robustness.md"
  },
  "launch_policy": "start_all_currently_launchable_with_rolling_capacity_refill",
  "machine_block_source_rendering": "deterministic_two_space_json",
  "runtime_rendering": "canonical_compact_json",
  "fan_in_owner": "coordinator",
  "additional_final_check_groups": [
    "gateway-lifecycle"
  ],
  "delivery_boundary": "Source implementation and isolated qualification only; live activation requires exact envelope. No PR publication."
}
```
<!-- codex-section:end id="spec.mcpjungle-gateway-robustness#plan.implementation.001" -->

<!-- codex-section:begin id="spec.mcpjungle-gateway-robustness#task.gateway.001" -->
```json
{
  "schema_version": 1,
  "kind": "implementation",
  "profile_role": "builder",
  "accountable_owner": "coordinator",
  "objective": "Implement and prove gateway against the frozen contract.",
  "context_refs": [],
  "read_scope": [
    "internal/model",
    "internal/migrations",
    "internal/service/mcp",
    "internal/api/mcp_servers.go",
    "internal/api/server.go",
    "internal/api/registration_contract.go",
    "internal/api/registration_contract_test.go",
    "internal/db",
    "cmd/start.go",
    "cmd/registration_guard_linux.go",
    "cmd/registration_guard_other.go",
    "cmd/registration_guard_test.go"
  ],
  "write_scope": [
    "internal/model",
    "internal/migrations",
    "internal/service/mcp",
    "internal/api/mcp_servers.go",
    "internal/api/server.go",
    "internal/api/registration_contract.go",
    "internal/api/registration_contract_test.go",
    "internal/db",
    "cmd/start.go",
    "cmd/registration_guard_linux.go",
    "cmd/registration_guard_other.go",
    "cmd/registration_guard_test.go"
  ],
  "requires": [
    "spec.mcpjungle-gateway-robustness#req.robustness.001"
  ],
  "produces": [],
  "dependencies": [],
  "dependency_inputs": [],
  "activation": {
    "mode": "automatic"
  },
  "acceptance_refs": [
    "spec.mcpjungle-gateway-robustness#ac.source.001"
  ],
  "test_refs": [
    "spec.mcpjungle-gateway-robustness#test.gateway.001"
  ],
  "affected_check_groups": [
    "gateway-lifecycle"
  ],
  "result_fields": [
    "summary",
    "evidence_refs",
    "produced_refs",
    "deferred_findings"
  ],
  "preserved_boundaries": [
    "Root sole Git owner; exact scoped writes; no live effects; preserve other edits"
  ],
  "stop_conditions": [
    "Unknown ownership, conflicting contract, broader protected effect or lost rollback"
  ]
}
```
<!-- codex-section:end id="spec.mcpjungle-gateway-robustness#task.gateway.001" -->

<!-- codex-section:begin id="spec.mcpjungle-gateway-robustness#test.gateway.001" -->
```json
{
  "schema_version": 1,
  "kind": "integration",
  "validates": [
    "spec.mcpjungle-gateway-robustness#req.robustness.001"
  ],
  "scenario": "Exercise gateway exact lifecycle and compatibility seams described in acceptance.",
  "evidence": {
    "predicate": "Named checks pass with exact source and owned fixture evidence.",
    "invalid": "Missing required cases, partial cleanup or implied live success.",
    "unknown": "Unavailable source, fixture outcome or ownership."
  },
  "environment": [
    "Isolated source worktree and disposable disk-backed fixtures"
  ],
  "obligation_owner_task": "spec.mcpjungle-gateway-robustness#task.gateway.001",
  "evidence_executor_task": "spec.mcpjungle-gateway-robustness#task.gateway.001",
  "acceptance_owner": "coordinator",
  "activation": {
    "after_accepted": []
  },
  "target_hints": [
    "internal/model",
    "internal/migrations",
    "internal/service/mcp",
    "internal/api/mcp_servers.go",
    "internal/api/server.go",
    "internal/api/registration_contract.go",
    "internal/api/registration_contract_test.go",
    "internal/db",
    "cmd/start.go",
    "cmd/registration_guard_linux.go",
    "cmd/registration_guard_other.go",
    "cmd/registration_guard_test.go"
  ],
  "affected_check_groups": [
    "gateway-lifecycle"
  ],
  "required_at": "checkpoint-and-candidate"
}
```
<!-- codex-section:end id="spec.mcpjungle-gateway-robustness#test.gateway.001" -->

<!-- codex-section:begin id="spec.mcpjungle-gateway-robustness#task.qualification.001" -->
```json
{
  "schema_version": 1,
  "kind": "implementation",
  "profile_role": "builder",
  "accountable_owner": "coordinator",
  "objective": "Implement and prove qualification against the frozen contract.",
  "context_refs": [],
  "read_scope": [
    "owner-plans/qualification.md"
  ],
  "write_scope": [
    "owner-plans/qualification.md"
  ],
  "requires": [
    "spec.mcpjungle-gateway-robustness#req.robustness.001"
  ],
  "produces": [],
  "dependencies": [
    "spec.mcpjungle-gateway-robustness#task.gateway.001"
  ],
  "dependency_inputs": [],
  "activation": {
    "mode": "automatic"
  },
  "acceptance_refs": [
    "spec.mcpjungle-gateway-robustness#ac.source.001"
  ],
  "test_refs": [
    "spec.mcpjungle-gateway-robustness#test.qualification.001"
  ],
  "affected_check_groups": [
    "gateway-lifecycle"
  ],
  "result_fields": [
    "summary",
    "evidence_refs",
    "produced_refs",
    "deferred_findings"
  ],
  "preserved_boundaries": [
    "Root sole Git owner; exact scoped writes; no live effects; preserve other edits"
  ],
  "stop_conditions": [
    "Unknown ownership, conflicting contract, broader protected effect or lost rollback"
  ]
}
```
<!-- codex-section:end id="spec.mcpjungle-gateway-robustness#task.qualification.001" -->

<!-- codex-section:begin id="spec.mcpjungle-gateway-robustness#test.qualification.001" -->
```json
{
  "schema_version": 1,
  "kind": "integration",
  "validates": [
    "spec.mcpjungle-gateway-robustness#req.robustness.001"
  ],
  "scenario": "Exercise qualification exact lifecycle and compatibility seams described in acceptance.",
  "evidence": {
    "predicate": "Named checks pass with exact source and owned fixture evidence.",
    "invalid": "Missing required cases, partial cleanup or implied live success.",
    "unknown": "Unavailable source, fixture outcome or ownership."
  },
  "environment": [
    "Isolated source worktree and disposable disk-backed fixtures"
  ],
  "obligation_owner_task": "spec.mcpjungle-gateway-robustness#task.qualification.001",
  "evidence_executor_task": "spec.mcpjungle-gateway-robustness#task.qualification.001",
  "acceptance_owner": "coordinator",
  "activation": {
    "after_accepted": []
  },
  "target_hints": [
    "owner-plans/qualification.md"
  ],
  "affected_check_groups": [
    "gateway-lifecycle"
  ],
  "required_at": "checkpoint-and-candidate"
}
```
<!-- codex-section:end id="spec.mcpjungle-gateway-robustness#test.qualification.001" -->

<!-- codex-section:begin id="spec.mcpjungle-gateway-robustness#ctx.sticking-points.001" -->
## Sticking points

Incident initiating cause remains unknown. Whole deadlines, cleanup latency, tombstone growth and supported startup load must be measured. Single authoritative gateway and old-launcher barrier are necessary activation constraints. Any inability to implement the frozen terminal/readiness/cleanup guarantee returns to judgment owner; never advertise contract 1 speculatively.
<!-- codex-section:end id="spec.mcpjungle-gateway-robustness#ctx.sticking-points.001" -->


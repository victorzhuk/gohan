# Tasks

Risk depth: existing-service-strict. No skipped, flaky or infrastructure-failed scenario passes. Allow one fixture repair and two diagnostic reruns per boundary. Stop on changed contracts or exhausted recovery limits.

## 1. Contracts

- [x] 1.1 Sync the approved delta, register the scenario IDs (`interop.client-deferred-registration`, `interop.client-guarding-intact`, `interop.client-description-drift`, `interop.client-awaiting-input-schema`, `interop.server-suspension-token-error`, `interop.server-awaiting-input-token`, `interop.server-suspensions-never-protocol-errors`) as proposed until admission (ADR-0171) and regenerate docs/design/types.md. Verify `task spec:types` and frozen method compatibility.
  Acceptance: `task spec:types` clean and the new ids resolvable in the spec.
  Evidence: `openspec/specs/interop/spec.md` is v1.4 (ADR-0183) with the seven requirements; `openspec/scenarios.json` holds all seven with `deferred_to: M3`; `task spec:types` reports no duplicates, undefined or uncatalogued identifiers; `openspec_validate` accepts the change. The delta's `input_required` "error" wording was synced as a reply because a JSON-RPC error has no `_meta` (design, Server decisions).
- [x] 1.2 Finalize the `adapter/mcp` module declarations (client options, server handler, mapping types) against the frozen `interop` and `tools` contracts before implementation. Verify no core package is touched and every frozen method is reused, not re-declared.
  Acceptance: module builds against frozen seams with a documented list of referenced frozen identifiers.
  Evidence: `openspec/changes/m3-interop-mcp/design.md` §Implementation contract. Referenced frozen identifiers: `types.ToolSpec`, `types.Tool`, `gohan.ToolPolicy`/`ApplyToolPolicy`, `gohan.NewTool`, `gohan.Deferred`, `std.ToolManifestHash`/`MarshalManifest`/`ParseManifest`/`CheckPinnedManifest`, `types.ErrManifestDrift`, `gohan.SuspendTool`, `types.SuspendError`, `types.InputRequest`, `permission.ApprovalRequest`, `stores.ResumeInput`, `stores.Checkpoints`, `stores.Runs`, `stores.EventLog`, `types.Telemetry`, `gohan.Flow`, `types.Principal`, `types.WithPrincipal`, `types.RunInfo`, `types.ToolUse`, `types.CredentialSource`, `gohan.Waker`, plus the structural `ResumeChild` seam core already routes for chained suspensions. No file under `core/` or `std/` changed.

## 2. Client behavior

- [x] 2.1 Implement namespace-pinned registration of server tools as `Untrusted` + `Deferred` with `MaxEffect` capped at `ReadOnly` unless wiring raises it. Prove `interop.client-deferred-registration` with an exact-ID public acceptance subtest.
  Acceptance: scenario passes and registered tools appear as deferred in `Explain`.
  Evidence: `adapter/mcp/client_test.go`, subtest `interop.client-deferred-registration` green.
- [x] 2.2 Implement the guarding boundary: annotation-derived effect caps that may only lower effect, trust preserved as `Untrusted`, and schema and scope checks intact for imported tools. Prove `interop.client-guarding-intact` with an exact-ID public acceptance subtest.
  Acceptance: scenario passes; a server annotation cannot raise the effective effect.
  Evidence: `interop.client-guarding-intact` and `interop.annotations-never-raise` green; `AnnotationCap` folds the cap before `ApplyToolPolicy`, so a raise cannot survive.
- [x] 2.3 Implement manifest-bound descriptions and the fail-closed refresh: a catalog refresh that changes a pinned tool's description or hash disables the tool and increments `gohan.tool.manifest_drift`. Prove `interop.client-description-drift` and reuse `interop.catalog-ttl` assertions with an exact-ID public acceptance subtest.
  Acceptance: scenario passes and drift increments exactly once per changed tool.
  Evidence: `interop.client-description-drift`, `interop.catalog-ttl` and `interop.namespace-in-manifest` green; the counting telemetry stub observes one increment per disabled tool.
- [x] 2.4 Implement MRTR mapping: confirmations to `HumanApproval`, other `input_required` to `AwaitingInput` with the server schema, replay with `inputResponses` under the original `CallKey`. Prove `interop.client-awaiting-input-schema` and reuse `interop.mrtr-confirmation`, `interop.mrtr-replay` assertions with an exact-ID public acceptance subtest.
  Acceptance: scenario passes and the journal records one call entry per replayed round-trip.
  Evidence: `interop.mrtr-confirmation`, `interop.mrtr-replay`, `interop.client-awaiting-input-schema` and the loopback `interop.awaiting-input-roundtrip` green. The replay settles through core's chained-suspension seam, so the resumed round never re-enters the scheduler: a journaled call holds exactly one entry across the whole suspend/resume cycle and a `ReadOnly`-capped call holds none (design, contract consequences).
- [x] 2.5 Implement Tasks mapping to `AwaitingTool` with `subscriptions/listen` wake and `Waker` poll fallback, and ttl-driven manifest refresh. Reuse `interop.task-handle` and `interop.catalog-ttl` assertions.
  Acceptance: scenario assertions pass with no network access beyond the configured server.
  Evidence: `interop.task-handle` green. The pinned SDK exposes no task notification type and its `subscriptions/listen` stream carries only catalog and resource-change notifications, so completion arrives through the `Waker` poll and the notification attachment stays a named seam (design, Server decisions); no notification is faked.

## 3. Server behavior

- [x] 3.1 Implement one `Flow` as one stateless MCP tool with `In`/`Out` schemas and suspension surfaced as an `input_required` error carrying the resume token in `_meta.gohan.token`. Prove `interop.server-suspension-token-error` with an exact-ID public acceptance subtest.
  Acceptance: scenario passes and the token round-trips through the error reply.
  Evidence: `interop.server-suspension-token-error` green; the reply is an `input_required` result whose `_meta.gohan.token` carries the token, which is the only representable shape because a JSON-RPC error has no `_meta`.
- [x] 3.2 Implement the stateless retry path: `inputResponses` retry as `Resume(token, in)` with `Approver` set from the MCP OAuth principal as transport code. Prove `interop.server-awaiting-input-token` and reuse `interop.server-stateless-resume` assertions with an exact-ID public acceptance subtest.
  Acceptance: scenario passes across a simulated pod change with only `Checkpoints` and `Runs` state.
  Evidence: `interop.server-awaiting-input-token` and `interop.server-stateless-resume` green, including a retry against a second handler instance; invalid content returns a schema failure and `Flow.Resume` is not called.
- [x] 3.3 Implement non-suspension mapping: `AwaitingExternal`/`AwaitingBatch`/`Scheduled` to Tasks `working` status, never protocol errors; long flows through the Tasks extension with `Detached` runs and `EventLog`-backed status. Prove `interop.server-suspensions-never-protocol-errors` and reuse `interop.server-long-task` assertions with an exact-ID public acceptance subtest.
  Acceptance: scenario passes and no suspension reason other than approval/input reaches the wire as an error.
  Evidence: `interop.server-suspensions-never-protocol-errors` and `interop.server-long-task` green; a `Tasks()`-marked flow returns its task before completion, `tasks/get` moves `working` → `completed`, and the ordered reader reports gap-free increasing `Seq`.

## 4. Integration

- [x] 4.1 Run assigned scenarios, targeted race checks on client registration and concurrent refresh, and a client-server loopback acceptance run on the 2026-07-28 binding. Retain assertion-level evidence, process deadlines and worker bounds. Unit tests require named residual risks beyond acceptance coverage.
  Acceptance: all registered scenario ids green with bounded runs and retained evidence.
  Evidence: `go -C adapter/mcp test -race -timeout 240s ./...` green; the load-bearing subtests are the 22 registry-shaped ones across `client_test.go`, `mrtr_test.go`, `server_test.go`, `tasksserver_test.go` and `loopback_test.go`. Named residual-risk tests: `UnsupportedMultipleInputRequests`, `ResumeChildRejectsSecondResume`, `ClientRequiresCheckpoints`, `TaskRunRecordsTerminalEvent`, `TasksGetUnknownTask`, `TasksMarkedFlowStartsDetached`. Gap, stated rather than hidden: `interop.awaiting-input-invalid` keeps its token-reuse clause only for a non-chained core suspension; through the chained seam the parent token is spent before the replay, so the adapter proves error-before-replay only, and core-local `AwaitingInput` schema validation stays an open follow-up (design, contract consequences).
- [ ] 4.2 Verify the standalone `adapter/mcp` module check, `task lint`, `task spec`, `task test` and `task api:check` after inspecting wrappers. Bound command duration and concurrency. Update the existing `[Unreleased]` entry for shipped behavior.
  Acceptance: gates green for the module scope with deadlines respected.
  Evidence: `task spec` green (`registered=789 subtests=2769 covered=713 missing=76 missing_in_scope=0 unregistered=0 failed_legs=0`); `task test` green across every workspace module including `adapter/mcp`; lint green with `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest run ./...` reporting `0 issues` because the `golangci-lint` binary is absent from this environment; `CHANGELOG.md` `[Unreleased]` carries the module entry. `task api:check` could not run: it creates a git worktree under `.git/worktrees`, and the execution environment mounts `.git` read-only (`fatal: could not create directory of '.git/worktrees/base': Read-only file system`). The new module has no release tag, so the checker skips it by its own rule; the root module API is unchanged by this change.

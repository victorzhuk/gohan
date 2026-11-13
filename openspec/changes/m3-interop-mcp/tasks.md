# Tasks

Risk depth: existing-service-strict. No skipped, flaky or infrastructure-failed scenario passes. Allow one fixture repair and two diagnostic reruns per boundary. Stop on changed contracts or exhausted recovery limits.

## 1. Contracts

- [ ] 1.1 Sync the approved delta, register the scenario IDs (`interop.client-deferred-registration`, `interop.client-guarding-intact`, `interop.client-description-drift`, `interop.client-awaiting-input-schema`, `interop.server-suspension-token-error`, `interop.server-awaiting-input-token`, `interop.server-suspensions-never-protocol-errors`) as proposed until admission (ADR-0171) and regenerate docs/design/types.md. Verify `task spec:types` and frozen method compatibility.
  Acceptance: `task spec:types` clean and the new ids resolvable in the spec.
- [ ] 1.2 Finalize the `adapter/mcp` module declarations (client options, server handler, mapping types) against the frozen `interop` and `tools` contracts before implementation. Verify no core package is touched and every frozen method is reused, not re-declared.
  Acceptance: module builds against frozen seams with a documented list of referenced frozen identifiers.

## 2. Client behavior

- [ ] 2.1 Implement namespace-pinned registration of server tools as `Untrusted` + `Deferred` with `MaxEffect` capped at `ReadOnly` unless wiring raises it. Prove `interop.client-deferred-registration` with an exact-ID public acceptance subtest.
  Acceptance: scenario passes and registered tools appear as deferred in `Explain`.
- [ ] 2.2 Implement the guarding boundary: annotation-derived effect caps that may only lower effect, trust preserved as `Untrusted`, and schema and scope checks intact for imported tools. Prove `interop.client-guarding-intact` with an exact-ID public acceptance subtest.
  Acceptance: scenario passes; a server annotation cannot raise the effective effect.
- [ ] 2.3 Implement manifest-bound descriptions and the fail-closed refresh: a catalog refresh that changes a pinned tool's description or hash disables the tool and increments `gohan.tool.manifest_drift`. Prove `interop.client-description-drift` and reuse `interop.catalog-ttl` assertions with an exact-ID public acceptance subtest.
  Acceptance: scenario passes and drift increments exactly once per changed tool.
- [ ] 2.4 Implement MRTR mapping: confirmations to `HumanApproval`, other `input_required` to `AwaitingInput` with the server schema, replay with `inputResponses` under the original `CallKey`. Prove `interop.client-awaiting-input-schema` and reuse `interop.mrtr-confirmation`, `interop.mrtr-replay` assertions with an exact-ID public acceptance subtest.
  Acceptance: scenario passes and the journal records one call entry per replayed round-trip.
- [ ] 2.5 Implement Tasks mapping to `AwaitingTool` with `subscriptions/listen` wake and `Waker` poll fallback, and ttl-driven manifest refresh. Reuse `interop.task-handle` and `interop.catalog-ttl` assertions.
  Acceptance: scenario assertions pass with no network access beyond the configured server.

## 3. Server behavior

- [ ] 3.1 Implement one `Flow` as one stateless MCP tool with `In`/`Out` schemas and suspension surfaced as an `input_required` error carrying the resume token in `_meta.gohan.token`. Prove `interop.server-suspension-token-error` with an exact-ID public acceptance subtest.
  Acceptance: scenario passes and the token round-trips through the error reply.
- [ ] 3.2 Implement the stateless retry path: `inputResponses` retry as `Resume(token, in)` with `Approver` set from the MCP OAuth principal as transport code. Prove `interop.server-awaiting-input-token` and reuse `interop.server-stateless-resume` assertions with an exact-ID public acceptance subtest.
  Acceptance: scenario passes across a simulated pod change with only `Checkpoints` and `Runs` state.
- [ ] 3.3 Implement non-suspension mapping: `AwaitingExternal`/`AwaitingBatch`/`Scheduled` to Tasks `working` status, never protocol errors; long flows through the Tasks extension with `Detached` runs and `EventLog`-backed status. Prove `interop.server-suspensions-never-protocol-errors` and reuse `interop.server-long-task` assertions with an exact-ID public acceptance subtest.
  Acceptance: scenario passes and no suspension reason other than approval/input reaches the wire as an error.

## 4. Integration

- [ ] 4.1 Run assigned scenarios, targeted race checks on client registration and concurrent refresh, and a client-server loopback acceptance run on the 2026-07-28 binding. Retain assertion-level evidence, process deadlines and worker bounds. Unit tests require named residual risks beyond acceptance coverage.
  Acceptance: all registered scenario ids green with bounded runs and retained evidence.
- [ ] 4.2 Verify the standalone `adapter/mcp` module check, `task lint`, `task spec`, `task test` and `task api:check` after inspecting wrappers. Bound command duration and concurrency. Update the existing `[Unreleased]` entry for shipped behavior.
  Acceptance: gates green for the module scope with deadlines respected.

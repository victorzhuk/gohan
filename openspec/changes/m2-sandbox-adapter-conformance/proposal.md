# Proposal: m2-sandbox-adapter-conformance

## Why

`openspec/changes/m2/design.md` §2.3 assigns four sandbox ids to this change — `sandbox.egress-denied`, `sandbox.egress-allowlisted`, `sandbox.exec-timeout` and `sandbox.egress-shares-policy-type` — and `2026-10-07-m2-sandbox-governance` shipped the port, the policy and the governed tool set while explicitly leaving adapter enforcement to it (`openspec/changes/archive/2026-10-07-m2-sandbox-governance/proposal.md:49`). The capability spec already names the vehicle: "Conformance: `sandboxtest` exercises every rule against an adapter with a deny-all egress probe" (`openspec/specs/sandbox/spec.md:77`). No such package exists: `testkit/` carries `storetest`, `gohantest` and `conformance` and no sandbox suite.

Without one shared suite, `adapter/docker` and `adapter/e2b` would each write their own egress, timeout and policy-shape tests, and the isolation layer — the part of the sandbox that no governance test can reach — would be accepted twice against two different bars, or not at all. `std/sandbox` classifies only the effect, secrets, outputs, budget and lifecycle; egress enforcement, the `MaxExecTime` process-group kill and the private-range rule are the adapter's network-layer obligation (`openspec/changes/archive/2026-10-07-m2-sandbox-governance/design.md:72,101`). Only a suite that runs unchanged against an adapter can hold that obligation.

## What Changes

- Add the conformance package `testkit/sandboxtest` beside `testkit/storetest` and `testkit/conformance`. It exports one suite, `sandboxtest.Sandbox(t, factory)`, whose subtests are named exactly the four owned normative scenario ids and run against an injected `types.Sandbox` implementation.
- Add the `Factory` seam: the adapter under test supplies a fresh `types.Sandbox` per test, a `Probe` declaring the argv shapes it can run inside a workspace (dial, sleep, echo), and a `Recorder` that receives the denial and cold-start observations the suite asserts. The suite hard-codes no provider, no image, no socket and no API key.
- Cover egress: a deny-all policy makes the dial probe fail inside the workspace; an allowlisted policy lets `api.example.com` connect and denies another host. Cover timeout: a probe that exceeds `MaxExecTime` has its process group killed and the outcome is classified `Unknown` under a `SideEffect` policy and `Failed` otherwise. Cover policy shape: the sandbox tool set and a harness tool render the same `types.EgressPolicy`, and code inside the workspace cannot reach a private range.
- Wire the suite into `adapter/docker` (`m2-docker-sandbox`) and `adapter/e2b` (`m2-e2b-sandbox`) as the acceptance bar each adapter must pass unchanged; neither adapter may keep a private copy of an owned scenario.
- Bind every task to an existing normative scenario id. No requirement text and no scenario id changes: the suite verifies behavior the `sandbox` capability already states, so this change carries no spec delta (`skip_specs: true`).

Explicitly out of acceptance: real provider execution. No installed module provides a sandbox, and Docker-socket presence and E2B credentials do not establish daemon liveness or a provider contract (`openspec/changes/m2/design.md` §2.3; `m2-sandbox-lifecycle-resume` design R6, which stays parked). This change's own acceptance is the suite compiling and passing against an in-memory fake `Sandbox`; the Docker and E2B legs are dependencies that stay blocked until those adapters land with provider evidence. `sandbox-lifecycle-resume`'s R6 provider admission stays parked and is not reopened here.

## Capabilities

### New Capabilities

- (none)

### Modified Capabilities

- (none)

## Impact

Affected code: `testkit/sandboxtest` (new), its suite tests, and the `_test.go` binding files in `adapter/docker` and `adapter/e2b`. Two seam questions the suite surfaces are recorded as open items in `design.md` and are not settled during authoring: the denial counter's canonical name, where the normative spec names `gohan.sandbox.egress_denied` (`openspec/specs/sandbox/spec.md:79,99`) and the shipped classification path counts `gohan.egress.requests{decision=denied}` (`core/tool_call.go:183-196`, ADR-0156); and the `Explain` policy-shape rendering the scenario names, where the shipped `chains.ToolInfo` carries no egress field (`core/chains/explain.go:38-41`). Each needs a bounded slice or an ADR-level reconciliation before it can be closed, so the suite asserts the observable the spec names and the open item records the exact gap rather than inventing a field or a metric.

No port change: `Sandbox` and `Workspace` keep their frozen method sets (`docs/design/compatibility.md:16`). No `std/sandbox` behavior change: effect derivation, secrets, outputs, budget and lifecycle stay with their owners. No adapter implementation: `adapter/docker` and `adapter/e2b` are separate changes. No provider execution in this change's acceptance.

Dependencies: `m2-sandbox-governance` (shipped), `m2-docker-sandbox` and `m2-e2b-sandbox` (the adapters that run the suite), and `m2-sandbox-lifecycle-resume` (whose R6 provider admission stays parked).

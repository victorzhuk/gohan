# Proposal: adk-go runtime backend

## Why

ADR-0002 names three runtimes from the start: native, eino and adkgo. Native and eino are delivered; `adapter/adkgo` is missing, so the conformance contract that names all three backends is unmet and the third-backend legs `runtime.conformance-adkgo` and `engines.adkgo-runtime-conformance` cannot run.

## What Changes

- Add `adapter/adkgo`, a module targeting `google.golang.org/adk/v2` (ADR-0061), implementing the runtime backend, message and tool bridges, and the workflow flows.
- Make it the third backend of the shared conformance suite beside native and eino, including the message-fidelity leg table that currently marks `adkgo` as pending.
- Settle Q3 (`docs/overview.md` 13): where the backend lacks a capability, gohan falls back to `Replay` (step re-execution over recorded results, `engines.replay-is-step-re-execution`), verified against the v2 tag pinned in `adapter/adkgo/go.mod`.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `engines`: the adk-go backend, its bridges and workflow flows, its conformance legs and the `Replay` fallback rule.

The delta also touches the `runtime` capability: its conformance requirement is the one that names the three backends. A second delta file `specs/runtime/spec.md` inside this change directory carries that modification.

## Impact

New module `adapter/adkgo` with its own `go.mod` pinning the adk-go v2 tag. Depends on the frozen `runtime` ports (`Runtime`, `AgentRun`, `State.Backend`, `StepGranularity`) and the shared conformance contract in `testkit/conformance`. No core or std change; `flow.backend-swap` and `limits.limits-under-foreign-backend` gain their third-backend legs without changing existing test code.

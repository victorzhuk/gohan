# External engines, dedup and run states

Capability: `engines` · Spec v1.0 baseline (restructured from gohan-spec v0.13; ADR-0075 owns composition-first engine integration) · Source of truth for this capability.

> **Contract tiers.** Code blocks in *Contract* are **normative** (ports, interfaces, error classes, stored shapes, event types, ordering rules) unless a block is marked `(illustrative)`, in which case names may change during M0 without a spec change. Prose rules in *Contract* are normative. Requirements are normative and each scenario ID is referenced by a test.

## Purpose

See `docs/overview.md` for how `engines` fits the architecture. Out of scope for this capability is anything owned by another capability spec; cross-references are by capability name.


## Contract

### 7.8 External engines (composition first)

The engine owns the business process; gohan owns bounded runs.

| Engine | gohan call | Owned by engine | Owned by gohan | Dedup |
|---|---|---|---|---|
| Kafka consumer | `Invoke` per message (or `Resume` for correlated callbacks) | delivery, offsets, retries, DLQ | the run, its journal, audit, limits | `OperationID` = message key or business ID (ADR-0077); journal fingerprints inside the run |
| Temporal | `Invoke`/`Resume` inside an activity; suspension returned as a typed activity result; approvals arrive as signals/updates that call `Resume` | workflow history, timers, process retries, compensation, continue-as-new | same | `OperationID` = workflow ID + step; activity retries hit `ErrOperationExists` and read the result |
| Camunda / Zeebe | `Invoke` from a job worker; `HumanApproval` → BPMN user task; `Resume` from the task completion | process state, timers, incidents | same | `OperationID` = process instance + element; duplicate workers after job timeout hit `ErrOperationExists` or `ErrRunActive` |

Under `WithHost(engine)`, `Waker` and `Recover` remain gohan's for the *run* only; process-level retries and timers are never re-implemented. Engine-driven loops (engine calling `Step` inside activities) are a later mode over ADR-0076.


## Requirements

### Requirement: Composition, dedup, states and flags

Engine integration MUST keep the business process owned by the engine, the run owned by gohan and deduplication keyed by `OperationID`.

#### Scenario: redelivery returns the same result
ID: `engines.redelivery-returns-the-same-result`
- WHEN `Invoke` with `OperationID` X completes and the same X is invoked again
- THEN `ErrOperationExists{RunID}` is returned before any component runs and `ByOperation` yields the stored result

#### Scenario: duplicate workers
ID: `engines.duplicate-workers`
- WHEN two workers invoke the same `OperationID` concurrently
- THEN exactly one runs; the other gets `ErrRunActive` or `ErrOperationExists`

#### Scenario: crash after consume
ID: `engines.crash-after-consume`
- WHEN the process dies after `Checkpoints.Consume(t, in)` and before the backend resumes
- THEN `Recover` finds the run `Resuming`, reads `PendingInput`, and re-drives it exactly once

#### Scenario: suspended runs are not reclaimed
ID: `engines.suspended-runs-are-not-reclaimed`
- WHEN a run is `Suspended` for three days awaiting approval
- THEN `Stale` never lists it and its lease is released

#### Scenario: revoked authority on resume
ID: `engines.revoked-authority-on-resume`
- WHEN the originator's scope was revoked while suspended
- THEN the scope check on resume denies the pending call

#### Scenario: frozen flag on replay
ID: `engines.frozen-flag-on-replay`
- WHEN a rollout flag changed while a run was suspended
- THEN resume uses the snapshotted value; `Explain` shows the pinned variant

#### Scenario: live deny beats approval
ID: `engines.live-deny-beats-approval`
- WHEN an operator approves `refund` and the `refunds.kill` live flag is on
- THEN the tool does not execute and the audit records `flag_denied` after `approved`

#### Scenario: stale control state
ID: `engines.stale-control-state`
- WHEN the flags provider is unreachable beyond `FreshnessLimit`
- THEN the next non-`ReadOnly` effect suspends with `AwaitingControl`; WHEN `MaxControlWait` elapses THEN the effect is denied

#### Scenario: definition pinned across deploy
ID: `engines.definition-pinned-across-deploy`
- WHEN a Lisp definition changes between suspension and resume
- THEN the run resumes on its pinned version and `gohan.definition.version` shows it

#### Scenario: expression cannot reach a host call
ID: `engines.expression-cannot-reach-a-host-call`
- WHEN a definition expression attempts to call a tool
- THEN `Build` rejects the definition

#### Scenario: replay is step re-execution
ID: `engines.replay-is-step-re-execution`
- WHEN `Replay` runs on any runtime
- THEN `Step` is re-executed over recorded results and produces the same `State` sequence up to the suspension point

### Requirement: The adk-go backend is the third conformance backend
The `adapter/adkgo` module MUST implement the frozen runtime surface for `google.golang.org/adk/v2` and MUST pass the shared conformance suite beside `native` and `eino`. The backend MUST declare `StepGranularity: Turn` and MUST source component events and the turn counter from the governed decorators only.

#### Scenario: Conformance green on the third backend
ID: `engines.adkgo-runtime-conformance`
- WHEN `conformance.Runtime(t, newRuntime)` runs with the adk-go backend installed
- THEN every shared subtest passes on its public path with no scenario skipped and no adk-go-specific test outside the adapter module

#### Scenario: Message fidelity round trip
ID: `engines.adkgo-message-fidelity`
- WHEN the shared fidelity contract runs the adk-go codec functions as values
- THEN block order, every declared kind's origin and sequence, and every carried blob equal the input after a full round trip

#### Scenario: Foreign tool under the adk-go backend
ID: `engines.adkgo-foreign-tool`
- WHEN a governed gohan tool is bridged as an adk tool and runs under the adk-go runtime
- THEN its identity, trust marking, spec declaration and result mapping match the assertions the eino leg already makes, and a foreign adk tool called outside an adk invocation is refused rather than given a synthesized context

### Requirement: Suspension crosses into adk-go's own resume mechanism
The backend MUST carry a component's suspend signal through its own resume mechanism and back into `Suspended`, MUST propagate `ctx` unchanged into every component call, and MUST honour cancellation. Where the adk runner cannot deliver a resumed input to the backend's agent shape, the adapter MUST declare that capability in its fallback table and resume the parked calls by `Replay` rather than falling back silently.

#### Scenario: Suspend and resume through the backend
ID: `engines.adkgo-suspend-bridge`
- WHEN a run suspends inside an adk-go turn and resumes from the stored resume input
- THEN the backend re-drives the run from rebuilt state, the approved call executes once under its recorded id, and the suspension and turn boundaries match the eino leg

### Requirement: Backend gaps fall back to Replay
For any capability the backend carries no mechanism for, the adapter MUST record the step result and resume by `Replay` re-executing `Step` over recorded results, and MUST declare the fallback per capability rather than falling back silently.

#### Scenario: Replay fallback for a missing backend capability
ID: `engines.adkgo-replay-fallback`
- WHEN a run resumes from recorded results for a capability the backend does not carry natively
- THEN `Step` is re-executed over those recorded results producing the same `State` sequence up to the suspension point, and the adapter's declared fallback table names every such capability

### Requirement: Workflow flows pass the foreign-backend suites
The adk-go workflow flows MUST pass the backend-swap and limits suites with unchanged test code, and the shared suites MUST keep consuming the backend only through the public path.

#### Scenario: Flow backend swap on adkgo
ID: `engines.adkgo-workflow-swap`
- WHEN the one acceptance suite runs an adk-go graph flow beside the other backends
- THEN it passes with the same test code and no per-backend branch

#### Scenario: Limits under the adkgo backend
ID: `engines.adkgo-limits-foreign`
- WHEN the limits scenarios run on an adk-go graph flow
- THEN the same limits hold as under the native and eino backends

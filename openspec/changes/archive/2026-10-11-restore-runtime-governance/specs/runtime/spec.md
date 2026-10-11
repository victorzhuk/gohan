# Spec Delta

This delta ADDS requirements to the `runtime` capability. It does not restate or replace any existing requirement. The `Run lifecycle ordering`, `Graceful shutdown`, `Tool batch protocol`, `Runtime conformance` and `Component mixing` requirements, and the scenario ids they own, stay with their current packages; the new ids below are unused by another package.

## ADDED Requirements

### Requirement: Foreign per-call governed path

A foreign flow MUST run from the resolved native configuration. Every tool call a foreign run makes MUST take the same governed path the native batch protocol uses per call: the approval gate and the native budget reservation MUST run before the tool implementation. The composed tool chain MUST remain the only ordinary-tool journal wrapper, so a call is journaled exactly once.

#### Scenario: mixed stack builds and invokes
ID: `runtime.foreign-mixed-stack-invoke`
- WHEN a stack registers a native flow and a foreign flow over the same driver and a foreign run executes a governed tool call
- THEN the call passes the gate and the effect journal exactly once and the tool result reaches the model

#### Scenario: allowed foreign call
ID: `runtime.foreign-call-allowed`
- WHEN a foreign run calls an `Idempotent` tool the policy allows
- THEN the gate returns `Allow`, the journal records one `Completed` entry for the call key, and the tool runs once

#### Scenario: denied foreign call
ID: `runtime.foreign-call-denied`
- WHEN the gate denies a foreign run's `SideEffect` call
- THEN the model receives a `Failed(Permanent)` result reading `not_executed:` and the tool implementation never runs

### Requirement: Foreign call identity is required

A foreign call that carries no stable `ToolUse` identity MUST fail closed with a `Failed(Permanent)` result before the gate, the reservation and any side effect, and MUST NOT be journaled.

#### Scenario: missing call identity fails closed
ID: `runtime.foreign-call-identity-required`
- WHEN a foreign backend delivers a tool call with no stable call id
- THEN the call fails closed before the gate and before any side effect, and no journal entry is written

### Requirement: Foreign call identity

A foreign backend MUST supply a stable call id per tool call, and the adapter MUST carry it into the governed call unchanged. The id on the governed `ToolUse`, the journal call key and the appended `ToolResult` MUST agree, so a resumed or recovered turn matches the completed call instead of re-executing it. Two calls to one tool in one turn MUST stay distinct under their own ids.

#### Scenario: call id round trip
ID: `runtime.foreign-call-id-round-trip`
- WHEN a foreign backend calls a tool with id `call-1`
- THEN the governed `ToolUse`, the journal call key and the appended `ToolResult` all carry `call-1`

#### Scenario: two calls in one turn
ID: `runtime.foreign-call-id-distinct`
- WHEN one foreign turn carries two calls to the same tool with ids `call-1` and `call-2`
- THEN each is gated and journaled under its own id and neither result is attached to the other call

### Requirement: Foreign invocation context

The caller's identity, deadline and cancellation lineage MUST reach every foreign model and tool call. A foreign run started by `Send`, resumed by `Resume` and reclaimed by `Recover` MUST observe the caller's principal, credential, run identity and deadline. A derived context MUST preserve that lineage; it does not promise the same context object inside a shielded call.

#### Scenario: context preserved across entry paths
ID: `runtime.foreign-ctx-preserved`
- WHEN a foreign run is started by `Send`, resumed by `Resume` and reclaimed by `Recover`
- THEN each path carries the caller's identity, deadline and cancellation lineage into every model and tool call

### Requirement: Foreign cancellation stops the turn by effect

Cancellation MUST behave by effect. A `ReadOnly` call is not shielded, so cancellation reaches the call, the call aborts, and no later component of that turn runs. After a non-`ReadOnly` call begins its committed execution, the shield settles the call, the journal completes its result, and cancellation stops the components after settlement. Cancellation MUST NOT discard a committed result before journal settlement, and a `ReadOnly` call MUST NOT gain a journal requirement it did not have.

#### Scenario: cancellation stops the turn
ID: `runtime.foreign-cancellation`
- WHEN the caller's ctx is cancelled during a foreign turn
- THEN an unshielded `ReadOnly` call aborts and no further component of that turn runs, while a committed non-`ReadOnly` call settles and journals its result before the run stops

### Requirement: Foreign recovery replays a completed call

`Recover` MUST answer a completed non-`ReadOnly` call from the journal and MUST NOT re-execute it.

#### Scenario: completed call recovered without re-execution
ID: `runtime.foreign-completed-call-replayed`
- WHEN a foreign run crashes after a non-`ReadOnly` call completed and was journaled
- THEN `Recover` replays the result from the journal, does not re-execute the call, and finishes the run
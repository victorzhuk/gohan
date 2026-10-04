# Sub-flows

Capability: `subflows` · Spec v1.1 (ADR-0091) · Source of truth for this capability.

> **Contract tiers.** Code blocks in *Contract* are **normative** (ports, interfaces, error classes, stored shapes, event types, ordering rules) unless a block is marked `(illustrative)`, in which case names may change during M0 without a spec change. Prose rules in *Contract* are normative. Requirements are normative and each scenario ID is referenced by a test.

## Purpose

Defines what a flow invoked from inside another flow (`FlowAsTool`, `fork`, `MapReduce`) inherits, isolates and propagates: context, identity, effects, suspension, limits, cancellation and events. Everything typed, everything through existing seams. Out of scope: run-tree budget accounting (`identity`), the recipes themselves (`std/flow`), detached children (Q25).


## Contract

```go
func FlowAsTool[In, Out any](name string, f Flow[In, Out], opts ...ToolOption) Tool

type ChildLimits struct {
	MaxWallClock time.Duration
	CostShare    CostShare
}

type CostShare int

const (
	ShareRemaining CostShare = iota
	ShareFixed
)

type OnError int

const (
	FailFast OnError = iota
	Collect
)

type PartialError struct {
	Results []any
	Errors  []error
}
```

`RunLimits` gains `MaxDepth` (default 2) and `MaxParallelChildren` (default 8).

Rules:

1. **Isolation.** A child run starts with an empty history in a child session `<parent-session>/<callID>` on the same stores. Only the typed `In` crosses into the child; the parent's history, notes and tool results do not. Typed `Out` returns as the parent's tool result and passes `StageToolResult` guards like any tool result. `Notes` are per session; a child has its own.
2. **Identity.** The child runs under the parent's ambient `Principal`; its `RequiredScopes` are checked at the call boundary before the child's deciders run. `RootRunID` is inherited, `ParentRunID` set, `Depth = parent + 1`.
3. **Effect derivation.** `FlowAsTool` derives `ToolSpec.Effect` from the child's registered tools: `SideEffect` if any is `SideEffect`, else `Idempotent` if any is `Idempotent`, else `ReadOnly`; `flowdef` `call: flow` is the same. The parent's journal, permission gate and cancel shield therefore apply to the whole child. An explicit `WithEffect` may only tighten.
4. **Nested suspension.** When a child returns `*SuspendError`, the parent checkpoints with the child token in `Checkpoint.Child` and returns its own `*SuspendError` with the same `Reason` and `Payload`, a new parent token. `Resume(parentToken, in)` consumes the parent checkpoint, resumes the child with `in`, and continues the parent when the child finishes; both tokens are single-use. Depth of chaining is bounded by `MaxDepth`.
5. **Limits.** A child must declare `ChildLimits.MaxWallClock` ≤ the parent's remaining wall clock, else `Build` fails. `CostShare`: `ShareRemaining` (default) lets the child spend what the tree has left; `ShareFixed(x)` caps it at `x`. `MaxDepth` and `MaxParallelChildren` are checked at the call boundary; exceeding either is `*LimitExceededError`. `MaxTurns`/`MaxToolCalls` are per run; `MaxCost` and `MaxSandboxSeconds` are per tree.
6. **Fan-out failure.** `fork`, `MapReduce` and `std/flow` parallel recipes take `OnError`: `FailFast` (default) cancels siblings on the first error and returns it; `Collect` waits for all, returns partial results plus `*PartialError`. Refinement loops (`Judge`-driven retries) require an explicit iteration cap.
7. **Cancellation.** The parent ctx is the child's ctx; cancellation propagates, the `SideEffect` shield (`streams`) is unchanged inside the child. A child never outlives its parent in v1.
8. **Events.** Child events carry `EventMeta.ParentRunID` and `Depth` and are delivered in the parent's stream and `EventLog` interleaved by arrival; `Done` of the child is not the parent's `Done`.
9. **Recovery.** `Runs` records exist for every child; `Recover` reclaims the root and replays the tree (`identity.nested-spans-and-recovery`).


## Requirements

### Requirement: Isolation and typing

#### Scenario: fresh history
ID: `subflows.fresh-history`
- WHEN a parent with 30 messages of history invokes a `FlowAsTool`
- THEN the child's first model request contains only the child's static context and its typed `In`

#### Scenario: child session
ID: `subflows.child-session`
- WHEN the child appends to `SessionLog`
- THEN it writes to `<parent-session>/<callID>` and the parent's history version is unchanged

#### Scenario: output guarded
ID: `subflows.output-guarded`
- WHEN a child returns `Out` containing an instruction the `StageToolResult` guard rejects
- THEN the parent sees a guard block, not the payload

### Requirement: Effect derivation

#### Scenario: side effect dominates
ID: `subflows.effect-derived`
- WHEN a child flow registers one `SideEffect` tool among `ReadOnly` ones
- THEN the `FlowAsTool` spec is `SideEffect` and the parent's permission gate applies to the call

#### Scenario: tightening only
ID: `subflows.effect-tighten-only`
- WHEN `WithEffect(ReadOnly)` is applied to a child that derives `SideEffect`
- THEN `Build` returns an error

### Requirement: Nested suspension

#### Scenario: approval inside child
ID: `subflows.nested-approval`
- WHEN a child's `create_booking` needs `HumanApproval`
- THEN the parent's `Invoke` returns `*SuspendError{Reason: HumanApproval}` with a parent token and the child token is stored in the parent checkpoint

#### Scenario: resume routes to child
ID: `subflows.nested-resume`
- WHEN `Resume(parentToken, Approve)` is called on another pod
- THEN the child resumes, its tool runs under the originator principal, and the parent continues with the child's `Out`

#### Scenario: both tokens single-use
ID: `subflows.nested-tokens-single-use`
- WHEN the child token is presented directly after the parent token was consumed
- THEN `Resume` returns `ErrTokenConsumed`

### Requirement: Limits and failure

#### Scenario: depth limit
ID: `subflows.max-depth`
- WHEN a child at `Depth == MaxDepth` invokes a `FlowAsTool`
- THEN the call fails with `*LimitExceededError{Limit: "MaxDepth"}` before the child starts

#### Scenario: parallel children
ID: `subflows.max-parallel-children`
- WHEN `MapReduce` fans out 20 items with `MaxParallelChildren: 8`
- THEN at most 8 children run concurrently

#### Scenario: child wall clock
ID: `subflows.child-wall-clock`
- WHEN a child declares `MaxWallClock` greater than the parent's
- THEN `Build` returns an error naming both flows

#### Scenario: fixed cost share
ID: `subflows.fixed-cost-share`
- WHEN a child runs with `ShareFixed(0.10)` and exceeds it
- THEN the child aborts with `*LimitExceededError` and the parent receives it as the tool error; the tree budget is unaffected beyond the spent 0.10

#### Scenario: fail fast
ID: `subflows.fail-fast`
- WHEN one of five parallel children fails under `FailFast`
- THEN the other four are cancelled and the parent receives the first error

#### Scenario: collect partials
ID: `subflows.collect-partials`
- WHEN one of five parallel children fails under `Collect`
- THEN the parent receives four results and a `*PartialError` with one error

### Requirement: Propagation

#### Scenario: cancellation
ID: `subflows.cancel-propagates`
- WHEN the parent ctx is cancelled while a child runs a `ReadOnly` tool
- THEN the child stops at its next safe point and the parent observes cancellation

#### Scenario: child events in parent stream
ID: `subflows.child-events`
- WHEN a client consumes the parent's event stream
- THEN child events appear with `ParentRunID` set and `Depth == 1`, and only the parent emits the final `Done`

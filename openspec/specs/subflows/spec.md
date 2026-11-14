# Sub-flows

Capability: `subflows` · Spec v1.2 (ADR-0182) · Source of truth for this capability.

> **Contract tiers.** Code blocks in *Contract* are **normative** (ports, interfaces, error classes, stored shapes, event types, ordering rules) unless a block is marked `(illustrative)`, in which case names may change during M0 without a spec change. Prose rules in *Contract* are normative. Requirements are normative and each scenario ID is referenced by a test.

## Purpose

Defines what a flow invoked from inside another flow (`FlowAsTool`, `fork`, `MapReduce`) inherits, isolates and propagates: context, identity, effects, suspension, limits, cancellation and events. Everything typed, everything through existing seams. Out of scope: run-tree budget accounting (`identity`), the recipes themselves (`std/flow`), detached children (Q25).


## Contract

```go
func FlowAsTool[In, Out any](name string, f Flow[In, Out], opts ...ToolOption) Tool

type ChildLimits struct {
	MaxWallClock time.Duration
	CostShare    CostShare
	FixedCost    float64
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
5. **Limits.** A child must declare `ChildLimits.MaxWallClock` ≤ the parent's remaining wall clock, else `Build` fails. `CostShare`: `ShareRemaining` (default) lets the child spend what the tree has left; `ShareFixed` caps it at `ChildLimits.FixedCost`. `MaxDepth` and `MaxParallelChildren` are checked at the call boundary; exceeding either is `*LimitExceededError`. `MaxTurns`/`MaxToolCalls` are per run; `MaxCost` and `MaxSandboxSeconds` are per tree.
6. **Fan-out failure.** `fork`, `MapReduce` and `std/flow` parallel recipes take `OnError`: `FailFast` (default) cancels siblings on the first error and returns it; `Collect` waits for all, returns partial results plus `*PartialError`. Refinement loops (`Judge`-driven retries) require an explicit iteration cap.
7. **Cancellation.** The parent ctx is the child's ctx; cancellation propagates, the `SideEffect` shield (`streams`) is unchanged inside the child. A child never outlives its parent in v1.
8. **Events.** Child events carry `EventMeta.ParentRunID` and `Depth` and are delivered in the parent's stream and `EventLog` interleaved by arrival; `Done` of the child is not the parent's `Done`.
9. **Recovery.** `Runs` records exist for every child; `Recover` reclaims the root and replays the tree (`identity.nested-spans-and-recovery`).


## Requirements

### Requirement: Isolation and typing

A child run MUST start with an empty history in its child session, and its typed output MUST pass `StageToolResult` guards.

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

`FlowAsTool` MUST derive the tool effect from the child's registered tools, and an explicit `WithEffect` MUST only tighten it.

#### Scenario: side effect dominates
ID: `subflows.effect-derived`
- WHEN a child flow registers one `SideEffect` tool among `ReadOnly` ones
- THEN the `FlowAsTool` spec is `SideEffect` and the parent's permission gate applies to the call

#### Scenario: tightening only
ID: `subflows.effect-tighten-only`
- WHEN `WithEffect(ReadOnly)` is applied to a child that derives `SideEffect`
- THEN `Build` returns an error

### Requirement: Nested suspension

When a child returns `*SuspendError`, the parent MUST checkpoint the child token in `Checkpoint.Child` and return its own `*SuspendError` with the same `Reason`, `Payload` and a new token. `Resume(parentToken, in)` MUST consume the parent checkpoint, resume the child with `in`, and continue the parent when the child finishes. A depth-`d` suspension SHALL chain a token through every checkpoint up to the root; resume MUST consume one token per level, and all tokens MUST be single-use.

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

#### Scenario: depth-two suspension chains to root
ID: `subflows.suspension-depth-two-chain`
- WHEN a depth-2 child suspends with `AwaitingInput`
- THEN the depth-1 parent checkpoints with the depth-2 token in its `Checkpoint.Child`, the root checkpoints with the depth-1 token in its own, and `Invoke` returns `*SuspendError` with the same `Reason` and payload and the root's token

#### Scenario: resume consumes one token per level
ID: `subflows.resume-consumes-chain`
- WHEN the root's token from a depth-2 suspension is resumed with the input answer, and then the depth-1 token is resumed with the answer the depth-2 child needs
- THEN each `Resume` consumes exactly its own token, the depth-2 child continues and finishes, and the root completes only after both inner levels finish

#### Scenario: inner token after chain consumed
ID: `subflows.inner-token-consumed-by-chain`
- WHEN the depth-2 child's token is presented after the depth-1 level already consumed it during a chain resume
- THEN `Resume` returns `ErrTokenConsumed`

### Requirement: Limits and failure

A child MUST declare `ChildLimits.MaxWallClock` no greater than the parent's remaining wall clock, else `Build` fails. `CostShare`: `ShareRemaining` (default) lets the child spend what the tree has left; `ShareFixed` caps it at `ChildLimits.FixedCost`. `MaxDepth` and `MaxParallelChildren` are checked at the call boundary; exceeding either is `*LimitExceededError`. `MaxTurns`/`MaxToolCalls` are per run; `MaxCost` and `MaxSandboxSeconds` are per tree.

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
- WHEN a child runs with `CostShare: ShareFixed` and `FixedCost: 0.10` and exceeds it
- THEN the child aborts with `*LimitExceededError` and the parent receives it as the tool error; the tree budget is unaffected beyond the spent 0.10

#### Scenario: fail fast
ID: `subflows.fail-fast`
- WHEN one of five parallel children fails under `FailFast`
- THEN the other four are cancelled and the parent receives the first error

#### Scenario: collect partials
ID: `subflows.collect-partials`
- WHEN one of five parallel children fails under `Collect`
- THEN the parent receives four results and a `*PartialError` with one error

### Requirement: Fan-out accounting and collection

A parent fan-out SHALL collect child results keyed to their input positions regardless of completion order, and each child SHALL record its spent cost and final depth on its `Runs` record. The parent tree budget MUST reflect the sum of child spends, and `*PartialError` under `Collect` SHALL identify each failed child by input position. `MaxParallelChildren` SHALL bound the children running concurrently per fan-out wave. Queued children MUST start as running ones finish.

#### Scenario: results keyed to inputs
ID: `subflows.fanout-results-keyed`
- WHEN a `MapReduce` wave over inputs `[a, b, c]` completes `c` first, then `a`, then `b`
- THEN the parent receives results in input order and each result maps to its own input position

#### Scenario: per-child cost attribution
ID: `subflows.fanout-cost-attribution`
- WHEN three fan-out children spend `0.02`, `0.05` and `0.00` of the tree budget
- THEN each child's run records its own spend and the parent tree budget is reduced by exactly `0.07`

#### Scenario: per-wave concurrency
ID: `subflows.fanout-per-wave-cap`
- WHEN `fork` launches a second wave while four children of the first wave still run, with `MaxParallelChildren: 8`
- THEN the second wave starts until at most 8 children of the whole tree run concurrently, and the cap does not count finished children

#### Scenario: partial error names positions
ID: `subflows.fanout-partial-positions`
- WHEN children at input positions 1 and 3 of five fail under `Collect`
- THEN `*PartialError` reports two errors each identifying positions 1 and 3, and the results slice keeps all five positions with nil at the failed ones

### Requirement: Nested limit inheritance

A child's effective budgets SHALL be derived at the call boundary from the parent's remaining budget: wall clock `min(declared, parent remaining)`, cost `min(cost share, tree remaining)`, depth allowance `MaxDepth - parent depth`. Derived budgets MUST be recorded on the child run so `Recover` replays the tree with the same budgets, and exceeding an effective budget MUST abort only the child that holds it.

#### Scenario: nested depth allowance shrinks
ID: `subflows.nested-depth-allowance`
- WHEN a depth-1 parent with `MaxDepth: 2` invokes a child that itself invokes a `FlowAsTool`, which again invokes one
- THEN the depth-2 call fails with `*LimitExceededError{Limit: "MaxDepth"}` derived from `MaxDepth - parent depth`, before any depth-3 child starts

#### Scenario: nested cost inherits tree remaining
ID: `subflows.nested-cost-inheritance`
- WHEN a depth-1 child spent `0.30` of a `0.50` tree budget and its own child runs with `ShareRemaining`
- THEN the depth-2 child's effective budget is the tree's remaining `0.20`, and exceeding it aborts only the depth-2 child with `*LimitExceededError`

#### Scenario: nested wall clock inherits remaining
ID: `subflows.nested-wall-clock-inheritance`
- WHEN a depth-1 parent has 4 minutes of wall clock left and its child declares `MaxWallClock: 10m`
- THEN the child's effective wall clock is 4 minutes, recorded on the child run, and a resumed depth-2 child runs under the same recorded budget

### Requirement: Propagation

Parent cancellation MUST propagate to the child, and child events MUST carry `ParentRunID` and `Depth` in the parent's stream.

#### Scenario: cancellation
ID: `subflows.cancel-propagates`
- WHEN the parent ctx is cancelled while a child runs a `ReadOnly` tool
- THEN the child stops at its next safe point and the parent observes cancellation

#### Scenario: child events in parent stream
ID: `subflows.child-events`
- WHEN a client consumes the parent's event stream
- THEN child events appear with `ParentRunID` set and `Depth == 1`, and only the parent emits the final `Done`

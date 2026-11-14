# Design

## Context

ADR-0091 froze the sub-flow contract at depth 1: a child `*SuspendError` becomes a parent checkpoint with the child token in `Checkpoint.Child`, both tokens are single-use, `MaxDepth` (default 2) and `MaxParallelChildren` (default 8) are checked at the call boundary, `ChildLimits.MaxWallClock` must not exceed the parent's remaining wall clock, `CostShare` splits the tree budget, and fan-out takes `FailFast | Collect`. The existing scenarios prove each property with one parent and one child. M3 workflow processes chain sub-flows deeper and fan out concurrently, which raises three questions the depth-1 text does not answer: how a token chain behaves when more than one level suspends, what a child inherits when the parent itself is already spending an inherited budget, and what the parent receives from a fan-out beyond the error split.

## Goals / Non-Goals

Goals:

- Suspension and resume specified for arbitrary depth within `MaxDepth`.
- Limit inheritance: each child derives its effective budget from the parent's remaining budget at the call boundary.
- Fan-out collection: keyed results, per-child cost and depth accounting, per-wave concurrency cap.

Non-Goals:

- Detached children (Q25, post-v1).
- Changing any frozen contract from ADR-0091: tokens stay single-use, effect derivation, cancellation and event propagation are untouched.
- New port methods or stored shapes; `Checkpoint.Child` already exists.

## Decisions

1. **Chained checkpoints, one per level.** A depth-2 suspension stores the depth-2 child token in the depth-1 parent's `Checkpoint.Child`, and the depth-1 parent's token in the root's `Checkpoint.Child`. `Resume` at any level consumes exactly its own token and drives the level below; the full chain completes only when every level finishes. Each token remains single-use; presenting an inner token after its outer ancestor was consumed fails `ErrTokenConsumed`, already specified.
2. **Inheritance from remaining, not granted.** A child's effective wall clock is `min(declared, parent remaining)`, its cost budget is `min(cost share, tree remaining)`, and its depth allowance is `MaxDepth - parent depth`. Effective budgets are checked at the call boundary and recorded on the child run, so `Recover` replays the tree with the same budgets.
3. **Per-child accounting on fan-out.** Every fan-out child records its spent cost and final depth on its `Runs` record. Under `Collect`, the parent receives results in input order (positions keyed to the input slice, not completion order) plus a `*PartialError` whose per-error entry names the child position; per-child cost and depth are readable from the run tree, and the parent tree budget reflects the sum of child spends.
4. **Per-wave concurrency.** `MaxParallelChildren` bounds the concurrently running children of one fan-out wave; queued children start as running ones finish. It is not a process-lifetime cap.
5. **Obligations stay inline in the requirements.** The queueing rule lives in the `Fan-out accounting and collection` body: queued children MUST start as running ones finish; the cap is never a process-lifetime bound. The per-level resume bound lives in the `Nested suspension` body: resume consumes one token per level, bounded by `MaxDepth`. This design restates both; no obligation is dropped or relocated.

## Frozen contracts (task 1.2)

Frozen before fan-out. Coders implement these shapes; nothing here adds a port method.

```go
// core/types/child_limits.go
type CostShare int

const (
	ShareRemaining CostShare = iota
	ShareFixed
)

// ChildLimits bounds one sub-flow call. FixedCost is read only when
// CostShare is ShareFixed; ShareRemaining ignores it.
type ChildLimits struct {
	MaxWallClock time.Duration
	CostShare    CostShare
	FixedCost    float64
}

type OnError int

const (
	FailFast OnError = iota
	Collect
)
```

```go
// core/flow_tool.go
type SubflowOption func(*subflowConfig) error

func (s *Stack) FlowAsTool[In, Out any](name string, f Flow[In, Out], opts ...SubflowOption) (Tool, error)
func WithChildLimits(l types.ChildLimits) SubflowOption
func WithParentLimits(l types.RunLimits) SubflowOption
func WithChildEffect(e types.Effect) SubflowOption
func WithEffect(e types.Effect) SubflowOption
```

The constructor is stack-scoped because the parent's `RunLimits` (which carries `MaxDepth`, `MaxParallelChildren` and the declared wall clock) is reachable only through the stack that resolved them; a tool call receives no limits on its ctx. At call time it resolves `s.limits[types.RunInfoFrom(ctx).Flow]`, `WithParentLimits` wins when set, and a call with neither refuses before the child starts.

```go
// core/types/run_budget.go
type RunBudget struct {
	WallClock time.Duration
	Cost      float64
	Depth     int
}

func WithRunBudget(ctx context.Context, b RunBudget) context.Context
func RunBudgetFrom(ctx context.Context) (RunBudget, bool)
```

The derived budget rides the child ctx as `RunBudget`; the child's own suspension checkpoint persists it, so a resumed level reuses the recorded values instead of re-deriving them from a parent whose remaining budget moved.

```go
// std/flow/fork.go, std/flow/mapreduce.go
type FanOption func(*fanConfig)

func WithOnError(types.OnError) FanOption
func WithParallelism(n int) FanOption

func Fork[In, Out any](child Flow[In, Out], opts ...FanOption) gohan.Flow[[]In, []Out]
func MapReduce[In, Mid, Out any](child Flow[In, Mid], reduce func([]Mid) (Out, error), opts ...FanOption) gohan.Flow[[]In, Out]
```

```go
// core/stores/runs.go — optional surface, discovered by type assertion
type RunCostRecorder interface {
	RecordCost(ctx context.Context, l Lease, cost float64) error
}
```

Derivation at the call boundary, in this order:

1. Depth: `parent.Depth+1 > MaxDepth` refuses before the child starts with `*types.LimitExceededError{Limit: "MaxDepth"}`.
2. Declared wall clock above the parent's declared `MaxWallClock` fails `Build`, naming both flows.
3. Effective wall clock `min(ChildLimits.MaxWallClock, parent remaining)` from the parent's ledger (`WallClockLeft`), recorded on the child run.
4. Effective cost `min(share, tree remaining)`, where `share` is `MaxCost - TreeCost()` for `ShareRemaining` and `FixedCost` for `ShareFixed`.
5. Child spend is the `TreeCost()` delta across the call; it leaves the parent tree budget reduced by exactly that amount and is recorded on the child row through `RunCostRecorder` when the store implements it.

Chain shape: a suspending call inside a sub-flow writes the child token to `Checkpoint.Child` of the level above and returns that level's own `*SuspendError` with the same `Reason`/`Payload`; `Resume` at any level consumes exactly one token, drives the level below, and the root completes only after every inner level finishes. Every token stays single-use.

## Risks / Trade-offs

- Deep chains multiply checkpoint writes on the suspend path; bounded by `MaxDepth`, so at most a constant number per suspension.
- Keyed-by-input-order collection means the parent waits for the slowest child under `Collect`; `FailFast` remains the default for latency-sensitive paths.
- Derived effective budgets must be deterministic across pods or resume changes semantics; they derive only from persisted parent state.

## Migration Plan

Spec-first change. Registration of scenario ids and `task spec:types` run at admission (task 1.1); behavior lands in `core` and `std/flow` with acceptance subtests per scenario id. No API break: `ChildLimits`, `OnError`, `PartialError` keep their shapes.

No additional migration risk. Frozen contracts from ADR-0091 and the suspension capability are preserved verbatim.

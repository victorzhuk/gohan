# subflows delta

## ADDED Requirements

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

## MODIFIED Requirements

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

A child MUST declare `ChildLimits.MaxWallClock` no greater than the parent's declared wall clock, else `Build` fails; a declaration within that bound but above the parent's remaining time clamps to the remainder. `CostShare`: `ShareRemaining` (default) lets the child spend what the tree has left; `ShareFixed` caps it at `ChildLimits.FixedCost`. `MaxDepth` and `MaxParallelChildren` are checked at the call boundary; exceeding either is `*LimitExceededError`. `MaxTurns`/`MaxToolCalls` are per run; `MaxCost` and `MaxSandboxSeconds` are per tree.

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

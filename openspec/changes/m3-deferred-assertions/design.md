# Design

## Context

The scenario registry holds one entry per normative behavior, and `tools/spec_coverage.py`
counts a scenario as covered only when `go test -json` reports a passing subtest whose last
path segment equals the id exactly. `task spec:gate GATE=M3` therefore measures the
milestone: on the tree where all eight M3 child changes are archived it reports
`missing_in_scope=22`, and every one of the nineteen non-live entries is already normative
text in `openspec/specs/<capability>/spec.md`.

Two of the nineteen are known debts rather than oversights. `m3-subflows` recorded that the
seven `subflows.*` assertions "belong to a later M3 slice" while shipping the machinery, and
`taint.subflow-carries-origin` has been carried as a milestone conflict since M1. The rest
are requirements a capability spec declares and no change ever asserted.

## Goals / Non-Goals

Goals:

- One passing subtest per id, named exactly the id, in the package that owns the rule.
- A gate whose count falls only when a requirement becomes evidenced.
- Each requirement implemented, not asserted around.

Non-Goals:

- Editing requirement text. Nothing here changes a contract.
- The three `performance.live-*` legs; they are blocked admission in `m3-s3-loadtest`.
- Widening a port or adding a public surface for the sake of an assertion.

## Decisions

1. **The id is the test name.** The gate matches the last `/` segment of a subtest, so a
   requirement is evidenced only by `t.Run("capability.name", …)`. A shared table that
   emits ids at runtime is acceptable only when the emitted name is the id itself.
2. **Implementation follows the assertion.** Each group starts by reading the requirement
   against the shipped code. Where the code satisfies it, only the test lands; where it does
   not, the implementation is fixed in the owning package and the spec stays untouched.
   Writing the assertion first is what makes that judgement testable rather than assumed.
3. **Deferrals fall per id, never move forward.** After an id's subtest passes, its
   `deferred_to` is removed from `openspec/scenarios.json`. Re-deferring an id to a later
   milestone to make a gate pass is the one move this change forbids: it converts unevidenced
   scope into invisible scope.
4. **`taint.subflow-carries-origin` is resolved, not carried.** M1 recorded it as a conflict
   between the sub-flow and taint contracts. The change asserts the behavior the taint spec
   states, or records a superseding decision that names the spec sentence it replaces. A
   third silent deferral is not acceptable.
5. **Grouping follows ownership.** Sub-flows, taint and context assertions live with the
   `std/flow` and `core` fixtures that build nested stacks; release assertions sit beside
   `std/evals` and the release identity surfaces in `core`; the single-id capabilities are
   small enough to stay one task each. Each group owns its files outright, so groups proceed
   independently.
6. **`structured-output.constrained` is the parent assertion.** `m3-constrained-output`
   registered seven narrower ids around the negotiation path. The parent id asserts the
   scenario the structured-output spec names end to end; the seven stay as they are.

## Risks / Trade-offs

- **Machinery claimed, evidence absent.** Only the sub-flow group has a documented claim that
  its machinery exists; the other groups may find a genuine implementation gap, which turns a
  test task into a code task. Each group assesses before it asserts, so the task list is
  re-scoped with evidence instead of assumed.
- **Shared fixtures.** Sub-flow fixtures are used by passing tests today; a new assertion that
  reshapes them can disturb the archived assertions. Groups extend fixtures additively and
  keep the existing subtests untouched.
- **Model and context ids** name behavior whose owner package is not obvious from the registry
  entry alone; the task that owns them reads the spec requirement before choosing the
  package.

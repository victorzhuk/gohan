# Design: m1-route-latency-class

## Context

`openspec/specs/build/spec.md:20` names the router strategies (`Static`, `ByLatencyClass`, `Decider`, `Cascade`) and `openspec/specs/model/spec.md` declares `LatencyClass` plus class-based admission, but no requirement states what a strategy does to an endpoint list. `ByLatencyClass` is implemented: `route.WithLatencyClass` (`std/route/route.go:26-31`) sets the class and a classful flag, and construction applies a stable partition (`std/route/route.go:41-78`) that places matching endpoints before the rest. The M1 matrix recorded the policy as unimplemented; the gap is that the behaviour is neither specified nor accepted.

## Goals

Write the contract the implementation already satisfies, prove it on the public path, and correct the milestone record. Non-goals: any production change, a `Decider` or `Cascade` specification, per-request class resolution, and latency measurement.

## Decisions

1. **A `routing` capability, not a `build` clause.** The strategies order endpoints, which is not a build concern, and the scenario registry prefixes them `routing`. `build` keeps its strategy table unchanged; it points at the capability rather than defining semantics in a table cell.
2. **Stable partition is the specified shape.** Class preference is not sorting and not filtering: matching endpoints lead, everything else stays in declaration order behind them. This is what the code does and what keeps the other classes usable as fallback candidates.
3. **The invariants are normative, not prose.** Class selection may not drop endpoints, reorder within a group, or touch the caller's slice; the eligible view may not consume a half-open probe; a fallback may not move on after the first emitted chunk. Each is an observable behaviour a test can pin.
4. **Existing behaviour may pass immediately.** This is acceptance closure on a shipped path (existing-service-strict): the subtests assert what holds, and no red stage is manufactured.

## Risks

- Reading the stale matrix as a request for a second API would create a duplicate `ByLatencyClass` constructor beside the shipped option. Mitigation: the proposal names `WithLatencyClass` as the implementation and forbids aliases.
- A class-only filter would remove valid fallback endpoints and narrow `Fallback` to one class. Mitigation: the requirement keeps the nonmatching endpoints and the fallback scenario serves a different class.
- New scenario ids need a capability that owns them. Mitigation: the added `routing` requirement carries all five ids, and `openspec/scenarios.json` registers each once under `routing` with `deferred_to: M1`.

## Migration Plan

None. No stored shape, port or API changes; `docs/design/types.md` gains the `routing` capability when the delta is synced.

## Verification

`go test -short -timeout 2m ./std/route/... -v` prints one `--- PASS` line per id; `task lint`, `task test` and `task spec:gate GATE=M1` stay green with the five ids covered.

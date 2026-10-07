# Change: m1-route-latency-class

## Why

The `build` contract names four router strategies — `Static`, `ByLatencyClass`, `Decider`, `Cascade` — and the `model` contract declares the three latency classes, but no requirement states what `ByLatencyClass` does to an endpoint list. The implementation is already shipped (`route.WithLatencyClass`) and the M1 acceptance matrix records the policy as missing, so the milestone carries a gap that is really a specification gap: the behaviour exists, unproven and undefined.

## What Changes

- A `routing` capability with one requirement: the endpoint-ordering policies, their stable-partition semantics and the invariants class selection must not break.
- Five registered scenarios that drive the shipped `route.NewRouter`, `Candidates`, `Eligible` and `Fallback` on the public path.
- The M1 acceptance matrix row corrected: the policy is shipped, the gap was acceptance and specification.

No production code changes. No new router interface, strategy type or alias.

## Impact

- Affected specs: `routing` (added).
- Affected code: `std/route` tests only.
- Consumers see no API change; a service that already orders by latency class gains a written contract and acceptance evidence.

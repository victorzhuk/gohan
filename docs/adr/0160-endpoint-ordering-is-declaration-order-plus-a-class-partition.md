# 0160. Endpoint ordering is declaration order plus a class partition

Status: accepted

## Context

The build contract names four router strategies — `Static`, `ByLatencyClass`, `Decider`, `Cascade` — and the model contract declares the three latency classes with class-based admission, but nothing states what a strategy does to an endpoint list. `ByLatencyClass` shipped as `route.WithLatencyClass` and the milestone record claimed it was missing, because there was no requirement to point at and no acceptance test to run.

Three shapes were available:

- Specify the strategy as sorting the endpoints by class. Sorting throws away the caller's declaration order inside a class, which is the only preference a caller can express today.
- Specify it as filtering: only the selected class is routed. That removes the fallback candidates and makes one class preference a hard dependency on one endpoint type.
- Specify it as a stable partition: the selected class leads, everything else follows in declaration order.

## Decision

Endpoint ordering is **declaration order plus a class partition**. The router keeps the caller's declaration order as the base order; selecting a latency class partitions the endpoints into the matching group followed by the nonmatching group, both in declaration order. The nonmatching endpoints stay routable as fallback candidates. Selecting a class never mutates the caller's slice.

The strategy set lives in a `routing` capability, not in the build table: ordering endpoints is not a build concern, and the scenario registry already prefixes the behaviour `routing`. The build table keeps naming the strategies and points at the capability.

## Consequences

- A caller that names a latency class gets its endpoints first without losing the others; `Fallback` can still serve a different class when the preferred one is rate-limited.
- Adding `Decider` or `Cascade` later extends the same capability and must state the same invariants: no mutation of the caller's slice, no removal of candidates.
- Class preference is ordering only. Admission control by class stays in the model contract and is unchanged here.
- A test that asserts sorted order by class will fail: within a group the order is the caller's, not the class's.

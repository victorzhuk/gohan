# Tasks

Delivered in `test(routing): accept the endpoint ordering policies`. A task is done when its listed scenario IDs pass and `task lint`, `task spec` and `task test` are green.

## 1. Contract and registry

- [x] 1.1 Add the `routing` capability delta with the endpoint-ordering requirements and their five scenarios; register each id once in `openspec/scenarios.json` under capability `routing` with `deferred_to: M1`; record the decision in an ADR.
  - evidence: `openspec/changes/m1-route-latency-class/specs/routing/spec.md`; `openspec/scenarios.json` (`routing.*`); `docs/adr/0160-endpoint-ordering-is-declaration-order-plus-a-class-partition.md`
- [x] 1.2 Correct the M1 acceptance matrix row: the policy ships (`route.WithLatencyClass`), the gap was specification and acceptance.
  - evidence: `openspec/changes/m1/design.md` (router obligation row)

## 2. Acceptance on the public path

- [x] 2.1 Add `routing.static-order` and `routing.latency-class-order` subtests driving `route.NewRouter` with and without `WithLatencyClass`, asserting the policy order and the declaration order inside both groups.
  - evidence: `std/route/routing_scenarios_test.go`
- [x] 2.2 Add `routing.latency-class-no-match`, asserting the complete declaration order survives a selected class no endpoint carries, and assert class selection never mutates the caller's slice.
  - evidence: `std/route/routing_scenarios_test.go`
- [x] 2.3 Add `routing.latency-class-breaker-order`, asserting an open breaker drops the endpoint from `Candidates` and `Eligible` without changing the remaining order and that `Eligible` leaves a half-open probe unconsumed.
  - evidence: `std/route/routing_scenarios_test.go`
- [x] 2.4 Add `routing.latency-class-fallback`, asserting a rate-limited preferred class fails over to another class in policy order through `route.Fallback`.
  - evidence: `std/route/routing_scenarios_test.go`

## 3. Package gates

- [x] 3.1 Run the repository wrappers and resolve failures; verify `task lint` and `task test`.
  - evidence: driver-run `task lint` 0 issues; `task test` green across the workspace
- [x] 3.2 Validate the change; verify `openspec validate m1-route-latency-class --strict --no-interactive` and `task spec:gate GATE=M1` with the five ids covered.
  - evidence: driver-run `openspec validate m1-route-latency-class --strict --no-interactive`; `task spec:gate GATE=M1` reports the five `routing.*` ids covered

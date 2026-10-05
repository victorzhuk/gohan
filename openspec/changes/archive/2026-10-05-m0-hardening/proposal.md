# Proposal: m0-hardening

## Why

M0 built the contracts, the driver, the `std` components and the store ports, and every task row in `openspec/changes/m0-core/tasks.md` is checked. The hardening review (`plan.md` in this directory) found that several of those checked behaviours are not connected to the public execution path, and reproduced three runtime defects. Until they are repaired, the M0.5 v1-candidate declaration rests on evidence that does not exercise the frozen surface.

This change repairs the public behaviour of what M0 already claims, and makes the gates that certify it honest. It adds no capability.

## Scope

1. **Authority and identity.** A run keeps its originator's authority through crash recovery; a session's run can only be cancelled by its owner; durable run identity does not collide between conversation instances.
2. **Durable transitions.** A steer is persisted before it is acknowledged and reaches the next assembly; a resumed run's events are recorded before delivery so `Attach` can reconstruct them.
3. **Correctness under reuse.** Context assembly evaluates the caller's tool filter per turn instead of reusing a result keyed by a code pointer.
4. **Recovery completeness.** A consumed preempted checkpoint whose client died before resuming is recoverable; a recovered run can suspend again.
5. **Honest evidence.** Scenario coverage counts only executed passing subtests; the performance gate fails closed on missing measurements and measures at its documented iteration floor; the API comparison ignores a parameter rename.

## Non-goals

- No new capability, adapter or provider. M1 store and model adapters stay out.
- No change to the port signatures in `core/stores` — the ports are frozen at v1-candidate.
- The reusable governed native agent constructor (the largest gap: `Build` does not connect configured middleware to a public native path) is designed in this change but implemented in a later one. Its design is a precondition, not a deliverable here.
- Core policy ownership (`core/chains` limit middleware, `core/types` preset values) is recorded as an open decision requiring its own ADR. It is not silently changed here.
- `Explain` implementation is deferred to the change that lands the native constructor, because both must read one resolved configuration.

## Decisions

`design.md` in this directory holds the sealed decisions D1–D11 and the rejected alternatives. One of them changes a contract and needs an ADR: consumed preempted checkpoints become recoverable (ADR-0146).

## Definition of done

- Every row in `tasks.md` is checked, with its `verify:` command green.
- `task lint`, `task test`, `task spec` (types index and the M0.5 gate) and `task api:check` are green, and each is reported with its observed output.
- The reproduced defects have runtime regressions that fail before the repair and pass after: cross-instance run identity, captured-filter isolation, and consumed-preempted recovery.
- `docs/design/api-review-m0-5.md` no longer claims a passing gate it cannot show, and states which driver surface the offline processes exercise.

# The acceptance processes run offline in M0.5

Status: accepted · Origin: review round, 2026-10-04

## Decision

M0.5 owns the offline paths of the three acceptance processes — `examples/kafka-refunds`, `examples/temporal-travel`, `examples/camunda-invoice` — written against `core`, `std` and the memory stores with a scripted model, turning their `engines.*` contract scenarios green before the core API is declared v1-candidate. Their live-engine runs stay in M3 with the adapters they need. `engines.definition-pinned-across-deploy` and `engines.expression-cannot-reach-a-host-call` are deferred to M4 with `languages`.

## Context and evidence

ADR-0083 makes the three processes the gate of the core API review and `docs/overview.md` repeats it as the M0.5 exit, but `m0-core/tasks.md` gave M0.5 three lines with no example scaffolding while `m3/proposal.md` owned the examples, so the gate could not pass where it was written. The offline paths need no adapter: the store ports are exercised through their memory implementations, and `engines.replay-is-step-re-execution` needs only `Step` over recorded results.

## Considered options

- Freezing at M3 exit instead, superseding ADR-0083.
- Enumerating a smaller offline subset without writing the three services.

## Consequences

The freeze is reviewed against async shapes — redelivery, duplicate workers, revoked authority, stale control state — rather than against ports alone. M0.5 grows by three example services. The remaining `engines.*` scenarios are marked `deferred_to: M3`.

# ADR-0117: Compatibility policy — ports frozen at v1, optional interfaces, apidiff gate

Status: accepted · Origin: grill round 43 (2026-09-30)

## Decision

`docs/design/compatibility.md` is normative. Root module v0 through M2, v1.0.0 at M2 exit; adapters version independently and require the core version their conformance ran against; release order core → adapters. Every interface is a *port* (user/adapter-implemented, frozen at v1, extended only by optional interfaces with stated fallbacks) or a *handle* (gohan-implemented, may grow); `types.md` records the kind. Value structs are not comparable and use keyed literals; new behaviour is opt-in; `// Deprecated:` is the one permitted non-why comment; `task api:check` runs `apidiff` per module. `Runs.Preempted` becomes optional `PreemptedLister` and the subject-memory methods become optional `MemoryStore`, each with a fallback scenario.

## Context and evidence

The Go module-compatibility guidance forbids adding methods to interfaces others implement and recommends new interfaces plus type assertion, option types for functions and zero-value-safe struct growth. Three ports grew this week; without a stated policy the same edits after v1 would break every third-party store.

## Consequences

`stores` v1.3, `recovery` rule 3a, `working-state` v1.3 (`MemoryStore`, `ErrMemoryStoreRequired`), generator emits an *Interfaces* section, AGENTS.md rules and command, M0.5 task 33 and milestone row.

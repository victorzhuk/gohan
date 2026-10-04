# ADR-0109: Context values are a closed set with `(T, bool)` accessors and a propagation table

Status: accepted · Origin: grill round 35 (2026-09-30) · Amends ADR-0028

## Decision

Identity stays ambient in `ctx` (`Principal`, `Credential`, `Approval`, `RunInfo`, idempotency key, shared state) and nothing else may be added. Keys are unexported in `core`; setters are transport/harness-only and a depguard rule forbids `context.WithValue` outside `core` and `adapter/*`. Every accessor returns `ok` (`RunInfoFrom`, `IdempotencyKey` and `SharedState` change signature) and never panics. `ErrNoPrincipal` is raised once at `Send`/`Invoke`/`Resume`, before `RunStarted` and any store access. A normative table fixes what survives the cancel shield, `Detached`, `FlowAsTool`, `Waker`/cross-pod resume and the sandbox boundary; `Credential` is never copied across a boundary that needs re-authentication.

## Context and evidence

The Go guidance on context values (only optional, cross-cutting, request-scoped data; typed unexported keys; `(T, ok)` getters; explicit parameters for required inputs) matches gohan's identity model exactly: the principal is the "auth token consumed by middleware" case and the security property `identity.model-cannot-set-identity` depends on only transport being able to set it. Passing identity explicitly would double every signature and put identity back into tool schemas. The gaps were zero-value ambiguity in two accessors, unspecified key ownership, and no statement of which values cross which ctx boundary.

## Consequences

`identity` v1.1 rule 8 and six scenarios; `agui`/`working-state` `SharedState` signature; `tools` closed-set note; task 4 updated, four scenarios deferred to M1/M2.

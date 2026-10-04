# ADR-0105: Provider adapter contract

Status: accepted · Origin: review round 31 (2026-09-30)

## Decision

`model` defines `ErrorClass`, `ModelError{Class, Provider, Status, Code, RetryAfter, Err}` and a nine-point provider adapter contract: status/code → class mapping with `Retry-After` parsing; `Caps.Fidelity map[BlockKind]Fidelity` declared per adapter and gated at `Build`; cache behaviour per `CacheMode` with `Caps.CacheBreakpoints` and `Usage.CacheWriteTokens`; `ResponseSchema` never silently ignored; `Usage` always filled (`Estimated` when the provider gives none); streaming and timeout rules; `Extra` may not override governed fields; `Raw` round-trip; `conformance.Model` with a shared fixture set. `BlockKind` enum added to `messages`; `StageProvider` added to `guards`.

## Context and evidence

The failover table was keyed on a type no spec defined, the fidelity matrix had no field, and cache/schema/usage behaviour lived in one line of prose — every one a bug class a scripted model cannot reveal and M1's first real adapter would have hit.

## Consequences

Eight `model` scenarios; `messages`, `guards`, `testing.md`, `adapters.md` §7.5, M1 proposal, task 29 updated.

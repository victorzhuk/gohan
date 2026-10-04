# ADR-0124: Session index and governed titles

Status: accepted · Origin: grill round 50 (2026-09-30)

## Decision

`SessionIndex` is an optional interface on `SessionLog` holding `SessionMeta{Title, TitleLocked, Archived, Pinned, Flow, Kind, ForkedFrom, Messages, CreatedAt, LastActivity}`; `Append` maintains counts and last activity on the store clock; forks, sub-flow children and shadow sessions carry their `Kind` and are hidden from the default listing. `Stack.Sessions` and `Stack.UpdateSession` are owner-checked with cursor pagination; without the index `Sessions` returns `ErrSessionIndexRequired`. `Purge` skips pinned sessions and applies a separate retention to archived ones. Titles come from `std/flow.Title`, an `Extract` run as a governed, cost-tagged child run after the first `Done`, never on forks, never after a user rename.

## Context and evidence

Chat runtimes model a conversation as `{id, name, updatedAt}` with LLM-generated names after the first message, lists ordered by activity with cursor pagination, and rename/archive as the two mutations. gohan had sessions, forks, deletion and erasure but no owner-scoped listing or metadata, so every product would keep a parallel table and drift from forks and erasure.

## Consequences

`stores` v1.5 (`SessionMeta`, `SessionQuery`, `SessionPatch`, `SessionIndex`, `Stack.Sessions`/`UpdateSession`, `ErrSessionIndexRequired`, five scenarios), `flow` v1.3 (titles, two scenarios), `identity` v1.2 (rule 7 widened, one scenario); task 7; title flow M1.

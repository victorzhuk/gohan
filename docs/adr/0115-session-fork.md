# ADR-0115: Edit and regenerate are `ForkSession` + `Continue`

Status: accepted · Origin: grill round 41 (2026-09-30)

## Decision

`Stores.ForkSession(from, upTo)` creates a new session from a parent's prefix, same owner, `History.ForkedFrom` set, audited; refused under a live lease. Inheritance is fixed: grants no; notes and shared state copied; journal, checkpoints, runs no; forks are not dependent records and survive parent deletion. `Conversation.Continue` runs one assistant turn without appending input. Regenerate is fork-at-last-user-message + `Continue`; edit is fork-before-the-message + `Send`. `SessionLog` stays linear; the tree lives across sessions.

## Context and evidence

Branching-chat runtimes model edit and regenerate as a new run from a checkpoint in an append-only tree. gohan had no primitive, so each transport would copy messages by hand and lose IDs, audit continuity, notes and the no-grant-inheritance guarantee. Two primitives cover both product features and the agentic "keep going" case without changing the store contract.

## Consequences

`stores` v1.2 (`ForkPoint`, `ForkSession`, forking rule, four scenarios), `flow` v1.1 (`Continue`, `ErrEmptyHistory`, two scenarios), `permission` v1.1, `working-state` v1.2, `agui` v1.2 (one scenario each); tasks 7 and 23; `agui` mapping M4.

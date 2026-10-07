# Spec Delta

## Context

This delta MODIFIES the `stores` *Session index* requirement, which is where the handoff fields are observed. The main requirement block carries no normative prose and five scenario blocks; this delta is a strict superset: it reproduces all five scenarios unchanged and adds the handoff-persistence rule the requirement lacked. The change also edits the `stores` Contract: `SessionMeta` gains `Handoff HandoffContext` and `SessionPatch` gains `Handoff *HandoffContext` (`openspec/specs/stores/spec.md:210`, `:243`). The Contract blocks are edited in the same implementation commit and `docs/design/types.md` is regenerated, exactly as the egress and hedging packages declare their Contract edits. The archive merge MUST union the added rule and MUST keep every reproduced scenario.

## MODIFIED Requirements

### Requirement: Session index

`SessionMeta.Handoff` MUST carry the `HandoffContext` recorded when a run requests a human, and `SessionPatch.Handoff` MUST set it through `UpdateSession`. Both fields default to the zero value, so a stored session row and a request row stay compatible and no data migration is required.

#### Scenario: control in session index
ID: `stores.control-in-session-index`
- WHEN two sessions are `ControlHandoffRequested` and one is `ControlHuman`
- THEN `Sessions` with `Control: ControlHandoffRequested` returns the two, ordered by `LastActivity`, for an operator queue

#### Scenario: sessions listed by owner
ID: `stores.sessions-listed-by-owner`
- WHEN subject `u1` has three sessions and `u2` has two in the same tenant
- THEN `Stack.Sessions` for `u1` returns exactly the three with `Title`, `Messages` and `LastActivity`

#### Scenario: sessions ordered by last activity
ID: `stores.sessions-order-last-activity`
- WHEN a message is appended to the oldest of a subject's sessions
- THEN it is listed first and the cursor from the previous page still resumes without duplicates

#### Scenario: forks and children hidden by default
ID: `stores.sessions-default-excludes-forks-children`
- WHEN a subject has a primary session, a fork of it, a sub-flow child and a shadow session
- THEN the default listing returns the primary and the fork only, and `Kinds: [SessionChild]` returns the child

#### Scenario: purge respects pinned and archived
ID: `stores.purge-respects-pinned-and-archived`
- WHEN `Purge` runs with a 30-day threshold over a pinned session, an archived session 200 days old and an archived one 100 days old
- THEN the pinned session stays, the 200-day archived one is purged and the 100-day one stays

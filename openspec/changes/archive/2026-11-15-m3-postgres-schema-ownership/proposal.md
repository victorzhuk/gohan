# Proposal: Postgres schema ownership

## Why

ADR-0169 made the migration cycle a `stores` contract but left the operational side unowned: how deployments drive `Migrate` and `Maintain`, how expand and contract migrations skew across versions, and how the hot `runs` table stays small under load. ADR-0073 fixed the shape (time partitioning, `DROP PARTITION` retention, coalesced heartbeats, `SKIP LOCKED` reaping, the transaction lint); M3 delivers the contract behind it.

## What Changes

- Add `Migrate` options (`Schema`, `TargetVersion`) and the `gohan-postgres migrate` offline command.
- Add `Maintain` for partition creation, retention by `DROP PARTITION` and `SKIP LOCKED` reaping of dead runs with coalesced heartbeats.
- State the expand/contract skew rule: expand first, contract only after every reader serves the new shape.
- Extend the adapter lint that forbids holding a transaction across a model or tool call.
- Add `storetest.Migrations` and `storetest.Bloat` to the stores testkit.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `stores`: Postgres schema ownership, maintenance and testkit bounds.

## Impact

Owns `adapter/postgres`, the `gohan-postgres` command and the stores testkit. Extends the existing `Postgres contract` requirement without contradicting its scenarios. Related records: ADR-0169, ADR-0073, ADR-0125, ADR-0117, ADR-0166.

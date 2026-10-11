# Tasks: m2-postgres-migration-cycle-contract

A task is done when its listed scenario ID passes on a subtest named exactly the ID and its `verify:` command is green. This change edits no source and no test: `adapter/postgres/migrate_cycle_test.go:113` already carries `t.Run("stores.pg-migration-down-up-down", …)` on the public path through `postgres.Migrate`, and its assertions stay unchanged. The contract work is the delta, the registry entry, the main-spec sync and the index. `task spec:gate GATE=M2` reports milestone-wide state, not this lane's green gate; unrelated missing scenarios are reported separately.

## 1. Delta, registry and sync

- [x] 1.1 Author `specs/stores/spec.md` as a `MODIFIED Requirements` delta for `Postgres contract`: the complete requirement prose and all ten existing scenarios verbatim, plus one appended paragraph and the inserted scenario `stores.pg-migration-down-up-down`. No other requirement is touched. Verify `timeout 30s openspec validate m2-postgres-migration-cycle-contract --strict --no-interactive`.
- [x] 1.2 Add `stores.pg-migration-down-up-down` to `openspec/scenarios.json` beside the other `stores.pg-*` entries with `capability: stores`, `origin: m2-postgres-migration-cycle-contract`, `title: migration down-up-down`, `deferred_to: M2`; every existing entry, including the `R51` deferrals to `M3`, stays unchanged. Verify the file parses as JSON and the ID appears exactly once.
- [x] 1.3 Sync the delta into `openspec/specs/stores/spec.md`: append the paragraph to the `Postgres contract` prose and insert the scenario between `stores.pg-fresh-equals-upgraded` and `stores.pg-auto-migrate-refused-in-prod`. Verify `timeout 30s openspec validate --specs --strict --no-interactive`.
- [x] 1.4 Record the decision in the next unreserved `docs/adr/` record and regenerate `docs/design/types.md`. Verify `timeout 120s task spec:types` exits zero with no duplicate or undefined identifier.

## 2. Gate

- [x] 2.1 Run `timeout 30m task spec:gate GATE=M2` with Docker available and read the summary line: expect `missing_in_scope=0` and `unregistered=0` for this change, with `stores.pg-migration-down-up-down` counted as covered. Any unrelated missing scenario is reported separately, not attributed to this change.

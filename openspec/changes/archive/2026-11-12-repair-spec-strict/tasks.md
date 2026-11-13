# Tasks

## 1. Inventory

- [x] 1.1 Capture the `openspec validate --specs --strict` ERROR list as a worklist grouped by spec and requirement.
  Acceptance: The worklist records every reported ERROR and its spec and requirement.

## 2. Mechanical RFC 2119 verb insertion

- [x] 2.1 Insert `MUST` or `SHALL` into each flagged requirement body under ADR-0161.
  Acceptance: The diff shows no semantic, scenario, ID, identifier or contract change beyond the inserted verbs.
- [x] 2.2 Compare each repaired body with its existing contract.
  Acceptance: Review evidence identifies each requirement and confirms its meaning is unchanged.
- [x] 2.3 Consolidate the duplicate requirement headings validation reports. `performance` carries three identically named `Performance budgets` blocks, each holding one scenario; requirement names must be unique, so they become one requirement carrying all three scenarios.
  Acceptance: One `Performance budgets` heading remains and its three scenario blocks and `ID:` lines are byte-identical to the originals.

## 3. tool/exec

- [x] 3.1 Promote the existing `#### tool/exec` contract into `### Requirement: tool/exec`.
  Acceptance: The requirement has a normative body derived from the existing contract.
- [x] 3.2 Move the three already registered exec scenarios (`tools.timeout-kills-group`, `tools.output-cap`, `tools.env-allowlist`) from `### Requirement: Verification` into the `tool/exec` requirement.
  Acceptance: The requirement owns those scenario blocks with their IDs and existing subtests unchanged, and `openspec/scenarios.json` keeps the same rows.

## 4. Remaining zero-scenario requirements

- [x] 4.1 For every remaining requirement the validator names as having no scenario, bind a scenario drawn from an existing covering test.
  Acceptance: Each such requirement has a scenario whose ID is already registered and whose subtest exists; no test or scenario semantics change.

## 5. Over-long requirement bodies

- [x] 5.1 Enumerate every `>500 characters` warning with its capability and requirement index.
  Acceptance: The worklist names all 30 over-long bodies across 15 capabilities. The original count of 37 included six missing-keyword warnings and one scenario-format warning.
- [x] 5.2 Split each flagged body into separate requirements that each state one behavior.
  Acceptance: Every resulting body stays under 500 characters, every pre-existing requirement heading survives, and each scenario stays with the behavior it binds.
- [x] 5.3 Confirm the split changed no scenario, ID, registry row or test.
  Acceptance: `openspec/scenarios.json` is unchanged by this section and the diff moves only requirement headings and body text.

## 6. Gate

- [x] 6.1 Run `openspec validate --specs --strict`.
  Acceptance: The output reports 36 of 36 specs passed.
- [ ] 6.2 Run `task spec:types` and `task test`.
  Acceptance: Both commands pass.
  Blocked closure: `task spec:types` passes. `task test` passes the root, OTel, OpenAI, Anthropic, Eino and examples legs, then fails `TestClientConnect` in `adapter/docker` and `TestJournalTx`/`TestMigrations` in `adapter/postgres` because no Docker daemon socket exists in this environment. Closure requires one `task test` run on a Docker-enabled host; the repository-side half is recorded in the verification state below.

### Verification state

- OpenSpec 1.17.0 strict validation reports 36 passed and 0 failed.
- `task spec:types` passes. It regenerates two `CheckpointRuns` index rows from existing store edits.
- The approved export-fidelity test repair passes the focused scenario suite and the full `adapter/otel` leg. `task test` now passes root, OTel, OpenAI, Anthropic and Eino, then fails Docker `TestClientConnect` because `/var/run/docker.sock` is unavailable.
- `task lint` cannot execute because `golangci-lint` is unavailable.
- Contract-preservation review covered all 34 changed spec files. Conflicting summaries for run limits, tool error mapping, conditional egress policy and provider-key selection were restored to their existing contracts.
- Independent resolution review confirmed the restored prose in tools, stores, model, runtime, identity, sandbox, context and streams. Scenario review confirmed no lost, duplicated or reworded scenario content and an unchanged registry.
- Final binding review confirmed the URL scenario belongs to `Blobs`, cross-provider ID prose remains normative in `Contract`, and the performance body explicitly retains the zero-allocation obligation. The `tool/exec` Contract block remains authoritative for its full execution contract.
- `worklist.md` records every baseline finding and corrects the original warning-count error. Final strict validation reports 36 passed and 0 failed. `task spec:types` passes.
- Separate investigation identified a representation-only timestamp comparison failure in `fidRoundTrip`. The approved test-only repair normalizes copied span and metric timestamps to UTC, preserving exact instants and every non-time assertion without changing captured records. No production code changed.
- The unreached PostgreSQL leg was run separately and fails because its tests require unavailable Docker. The examples leg passes separately. Task 6.2 remains open until the full test command passes in a Docker-enabled environment. No commit or archive has been performed.
- Do not archive or mark the remaining acceptance items complete until preservation review and required gates pass.

## Ownership and sequencing

The `task test` closure needs a Docker daemon at `/var/run/docker.sock`; its absence is an environment condition, not a repository deliverable. `golangci-lint` is also absent from `PATH`, and lint is not part of the section 6 acceptance.

Package check: `openspec validate repair-spec-strict --strict`. This check validates the planning artifacts only. It supplies no external acceptance, activation, or publication evidence.

Archive this change with `--skip-specs` because it edits the main specs directly.

# M2 remediation index: confirmed findings to change packages

This file is the approved finding-to-change index for the second-wave audit. It
records which confirmed finding lands in which authored package, the sequencing
between packages, and the acceptance state before implementation starts. It is
not an implementation plan.

Scope rule: authoring only. No source fix, test run, migration, dependency bump,
main-spec edit, registry edit, ADR or generated index changed while this index was
written. No commit was made.

## 1. Packages

Six new packages, authored from the audit:

| Slug | Tasks | Capability | Registry delta |
|---|---|---|---|
| `restore-runtime-governance` | 17 | runtime, permission, build | none |
| `repair-conversation-lifecycle` | 23 | streams, runtime, flow, stores, identity | none |
| `protect-tool-output-storage` | 18 | sandbox, tools | none |
| `repair-postgres-store-contracts` | 32 | stores | none |
| `repair-egress-response-lifetime` | 16 | tools | none |
| `harden-workspace-security-gates` | 13 | none (`skip_specs: true`) | none |

Three second-wave packages, amended from their first authoring:

| Slug | Tasks | Amendment |
|---|---|---|
| `m2-context-persisted-compaction` | 33 | design D1–D14, tasks rewritten, context delta rewritten |
| `m2-eino-bridges` | 25 | bridge constructor contract, codec scope, evidence section, public-path harness |
| `m2-sandbox-lifecycle-resume` | 37 | coordinator placement, generation CAS, publish/release ordering, cleanup scope, admission blockers |

Total: 214 tracked tasks, 0 marked complete. Counts are read from each
`tasks.md` checkbox list.

## 2. Finding coverage

36 audit entries, 35 after de-duplication. One entry
(`core/native_conversation.go:27`) carries two claims about the same line and
resolves to one task.

Source line numbers are the audit's references. Every cited line was re-read in
the current tree except two, which drifted. Current anchors:
`core/native_resolve.go:173` (`s.native = resolved`, the replacement) instead of
`:179`, and `core/foreign_conversation.go:106,109` (the `foreignTools` comment
and signature) instead of `:105`. The `context.Background` substitution is
`core/foreign_conversation.go:72`.

### 2.1 `restore-runtime-governance`

| Finding | Line | Bound task |
|---|---|---|
| No permission gate or effect journal for foreign tools | `core/foreign_conversation.go:105` | 2.2 |
| Foreign run context replaced by `context.Background` | `core/foreign_conversation.go:79` | 3.1 |
| Caller context discarded in foreign run construction | `core/foreign_conversation.go:72` | 3.1 |
| Foreign resolution replaces native resolved flows | `core/native_resolve.go:179` | 4.1 |

### 2.2 `repair-conversation-lifecycle`

| Finding | Line | Bound task |
|---|---|---|
| Constructors omit the stack admission binding | `core/native_conversation.go:27` | 1.2 |
| Run admission and shutdown are not atomic | `core/shutdown.go:85` | 2.1 |
| First handoff branch makes the callback unreachable | `core/drive_lifecycle.go:485` | 3.3 |
| Terminal persistence forces completed over the real stop reason | `core/drive_lifecycle.go:977` | 3.1 |

### 2.3 `protect-tool-output-storage`

| Finding | Line | Bound task |
|---|---|---|
| Spilled sandbox output persists unredacted bytes | `std/sandbox/output.go:50` | 2.1 |
| `web_fetch` ignores `MaxOutput` and the store | `std/tool/http/fetch.go:32` | 4.2 |

Output sanitization belongs here and nowhere else. No other package claims the
sandbox spill path or the fetch cap contract.

### 2.4 `repair-postgres-store-contracts`

| Finding | Line | Bound task |
|---|---|---|
| One owner per content ref drops the second subject's claim | `adapter/postgres/outputs.go:43` | 2.1, 2.2 |
| Session index orders by id and filters after `LIMIT` | `adapter/postgres/session.go:442` | 3.1, 3.2 |
| Session delete cascade runs outside the delete transaction | `adapter/postgres/session.go:367` | 7.1, 7.2 |
| Journal reserve collides instead of returning the entry | `adapter/postgres/journal.go:252` | 4.1 |
| `Start` uniqueness check is a non-atomic select-then-insert | `adapter/postgres/runs.go:169` | 5.1 |
| Notice claim is an unlocked select-then-update | `adapter/postgres/runs_notices.go:231` | 6.1 |

This package owns its concrete store implementation only. It does not define the
compaction publication or audit-hash capabilities; those stay with
`m2-context-persisted-compaction` task 2.4, which carries the PostgreSQL append and
schema work for publications.

### 2.5 `repair-egress-response-lifetime`

| Finding | Line | Bound task |
|---|---|---|
| A new `http.Transport` per request | `std/egress/client.go:94` | 2.1 |

Resolution, not silence: the per-request transport stays by decision (design D2,
D6). The repair releases idle connections on every exit path through one
idempotent release closure (design D3). The absence of connection pooling is a
recorded non-goal, not an unaddressed finding. Per-request checked-address dialing
is preserved unchanged (task 2.2, design D4).

### 2.6 `harden-workspace-security-gates`

| Finding | Line | Bound task |
|---|---|---|
| Security gate scans the root module only | `.github/workflows/ci.yml:65` | 2.1, 4.1 |
| Milestone scenario gate pinned at `M0.5` | `Taskfile.yml:31` | 1.1, 5.2 |

The gate default stays `M0.5` (`Taskfile.yml:28-31`). Task 5.2 asserts that the
default and the M1 floor are unchanged. The scan enumerates the root module, the
five adapter modules and `examples`.

### 2.7 `m2-context-persisted-compaction`

Line numbers are the audited references. Both designs were rewritten, so the
current binding is named by decision.

| Finding | Audited line | Bound in |
|---|---|---|
| Coverage clamped after summarization; no pair boundary protection | `design.md:35` | D3, D8; tasks 4.2, 4.3 |
| Selected compaction not removed from the retained suffix; older records undefined | `design.md:31` | D9; tasks 3.1, 3.2 |
| Same-stage history, audit and event writes are not crash safe | `design.md:47` | D10; task 6.4 |
| Additive audit payload absent from the hash-chain change | `design.md:47` | D11; task 2.3 |
| Rejection fallback assumes a configured `Truncate` without the prerequisite | `design.md:43` | D5, D12; tasks 5.1, 5.2 |
| Counting source and token-trigger contract unresolved | `design.md:27` | D7; tasks 5.4, 6.3 |
| Two token measurements from a block with one | `tasks.md:68` | D7, D13; task 6.3 |

Task 6.3 separates the two counts: `Compaction.Tokens` is the summary's count,
`TokensBefore`/`TokensAfter` are the request's sizes.

### 2.8 `m2-eino-bridges`

| Finding | Audited line | Bound in |
|---|---|---|
| Constructor contract omits governance metadata source and result conversion | `tasks.md:9` | tasks 1.1–1.6 |
| Provenance command does not show the owned scenario running | `tasks.md:24` | tasks 4.1, 4.2 |
| Claimed inherited full-chain proof is absent from the cited test | `design.md:37` | design §6; task 4.1 |
| Excluding codec changes conflicts with new fidelity obligations | `design.md:29` | design §2; tasks 2.1–2.5 |

The evidence section replaces the removed inheritance claim with four owed items.
The message codec is in scope for provenance and per-kind fidelity only; the
backend call-id binding and the shared governed path stay with
`restore-runtime-governance` (design §2, tasks 5.1, 5.2).

### 2.9 `m2-sandbox-lifecycle-resume`

| Finding | Audited line | Bound in |
|---|---|---|
| Recovery restores an older ref over newer completed work | `design.md:58` | D5; tasks 2.4, 2.5 |
| Reaper has no coherent resource identity or cleanup boundary | `design.md:66` | D7; task 6.5 (unmet) |
| Snapshot persistence has no failure settlement | `design.md:43` | D6; tasks 4.2, 4.3 |
| Cleanup sandbox configuration unbound from tool configuration | `design.md:66` | D1; tasks 3.2, 3.3 |
| Suspend and detached-end requirements contradict the implementation | `specs/sandbox/spec.md:13` | D4; tasks 5.1, 5.2, 7.3 |

## 3. Sequencing

1. `restore-runtime-governance` before `repair-conversation-lifecycle`: both edit
   `core/foreign_conversation.go`. Lifecycle task 1.1 gates on the governance
   edit landing and asserts the governed wrapper and run context survive.
2. `restore-runtime-governance` before `m2-eino-bridges`: both edit
   `adapter/eino/runtime.go` and the governed call path
   (`m2-eino-bridges/design.md:37`).
3. `repair-postgres-store-contracts` before `m2-sandbox-lifecycle-resume` task 6.3:
   cross-pod acceptance needs the repaired `RunForSession` lookup first.
4. `protect-tool-output-storage` and `repair-egress-response-lifetime` are
   independent of each other and of the driver lanes.
5. `harden-workspace-security-gates` is independent of every other package.

## 4. Acceptance

- M1 is the regression floor. Every lane runs the M1 floor plus its own touched
  modules.
- `task spec:gate GATE=M2` is the global M2 milestone gate
  (`openspec/changes/m2/tasks.md:48-50`, milestone definition of done). It is not
  a per-lane green gate. Each lane reports its own owned scenario ids and the
  public paths they drive.
- A lane acceptance run names its owned scenario ids and public paths. A run that
  matches zero tests does not count.
- Every proposed scenario id is unregistered. Measured against
  `openspec/scenarios.json` (522 entries):

| Slug | Ids | Unregistered |
|---|---|---|
| `restore-runtime-governance` | 14 | 14 |
| `repair-conversation-lifecycle` | 27 | 27 |
| `protect-tool-output-storage` | 9 | 9 |
| `repair-postgres-store-contracts` | 19 | 19 |
| `repair-egress-response-lifetime` | 6 | 6 |
| `m2-context-persisted-compaction` | 24 | 17 |
| `m2-eino-bridges` | 10 | 7 |
| `m2-sandbox-lifecycle-resume` | 32 | 27 |

  The registered ids inside the three amended packages are pre-existing scenarios
  the deltas extend, not new rows. The 27 `repair-conversation-lifecycle` ids carry
  a literal `proposed.` prefix; it is a draft label, and each id still counts as one
  unregistered proposed id in the table above. Each package schedules its own
  registry edit, main-spec sync and types-index regeneration as apply-time tasks.
  No ADR number is reserved during authoring.

## 5. Sandbox admission blockers

Stated as unmet, not as designed:

1. No installed sandbox adapter module exists. Snapshot survival after `Close`,
   retention, missing-snapshot behaviour and close-by-resource-identity are
   undefined. `m2-sandbox-lifecycle-resume` task 6.5 stays unchecked.
2. `openspec validate stores` already fails on a pre-existing requirement
   (`Checkpoint versioning and quota pools`, no SHALL/MUST) outside this audit's
   edit scope. Task 7.2 coordinates that main-spec edit with the Postgres lane.
# Proposal: m0-public-path-closure

## Why

M0 and its hardening change are fully checked, and the M0.5 gate is green. The gate matches scenario IDs against passing subtest names, so it certifies that names exist and pass — not that the public execution path behaves as the contract says. A read-only audit against the shipped native entry found that it does not, in ways a scenario name cannot show.

Six behaviors are recorded as done and are not true of the code that runs when a service calls `Build` → `NewNativeConversation` → `Send`: a permission ask reports `AwaitingBatch` instead of `HumanApproval`, so an approval cannot be delivered through `Resume`; the settled results of a mixed batch are dropped at suspension; `MaxTurns` is not enforced on a native run; a resumed or recovered native run starts from a valid-looking but empty phase; assembly never receives the registered instruction, tools or profile; and `Done.Cost` omits what the run's ledger charged. Three stream protections are recorded as landed on the public path and are not: the run worker drains the bounded provider buffer into an unbounded slice, the stall guard measures producer silence, and `Resume` installs neither the worker nor the guard. Two architecture defects are also live: tool middleware ignores `Step.Applies`, and resolved definition slices remain caller-owned after `Build`.

One of these is a security defect and precedes any adapter work: `rejectReservedMeta` has no production caller, so a caller can append a message carrying the reserved `gohan.approval` metadata and the permission gate's grant lookup can treat it as an approval. Nothing in the workspace exercises the frozen surface far enough to notice.

The audit also found evidence that cannot be trusted as proof: the runtime and chain conformance suites certify a hand-written stand-in rather than the shipped native runtime, three `chains` scenarios are covered by tests that assert something narrower than the scenario, the gate tools' own Python tests have no CI leg and no Taskfile target, and the base/head performance gate the README presents is not the command CI runs.

## Scope

1. **Untrusted ingress.** Messages carrying the reserved approval metadata are refused before any append, signal or operator mutation, and a decoded receipt without approvers grants nothing.
2. **Run identity.** `Send` and `Continue` install the identity of the run they acquired, so a scoped tool sees a real run and journal keys are session-scoped.
3. **Stored authority.** Recovery that cannot resolve stored authority fails closed instead of executing with the reaper's ambient principal.
4. **Native execution.** Permission asks become `HumanApproval`; a mixed batch keeps its settled results across suspension, resume and recovery; `State.Turn` is authoritative and bounds model calls; the running phase is reconstructed from durable history; assembly receives the resolved configuration; `Done.Cost` reports the run's spend.
5. **Streams and stores.** One bounded delivery handoff, stall arming tied to the consumer callback, cleanup on abandonment, worker ownership for resumed runs, one durable failure terminal, terminal arbitration in the model stream, a correct wrapped-ring expiry, and cross-instance `Attach`.
6. **Ownership and evidence.** Tool steps honour their predicate against the resolved specification; resolved slices are owned by the stack; conformance and scenario coverage bind the public path; the gate tools and runnable examples run in CI; documentation and compatibility evidence match the code.

## Non-goals

- No new capability, adapter or provider. M1 stores and models stay out.
- No change to the frozen port signatures, the public checkpoint fields or the store schema version. ADR-0154 changes only the private `Checkpoint.Data` format, with legacy refusal.
- The public `EventMeta`/`Seq` stream envelope stays deferred (ADR-0152 decision 7); `Attach` gains no authorization rule here — the ownership question is recorded in `design.md`, not answered silently.
- No weakening of the sequential-approval or non-read-only durability requirements to match the code. The spec stays the source of truth.

## Decisions

`design.md` in this directory holds the sealed decisions and rejected alternatives. Two of them change recorded decisions and need ADRs: the conversation's bounded delivery handoff and resumed worker (ADR-0153, amending ADR-0152), and the native continuation record with per-ask approvals (ADR-0154).

## Definition of done

- Every row in `tasks.md` is checked with its `verify:` command green, and each wave is reported with its observed output.
- The public-path regressions reproduce their defect before the repair and pass after: receipt ingress, minted run identity, mixed-batch continuation, three-ask sequential approval, `MaxTurns` through `Send`, running recovery, bounded delivery, stall arming, resume failure terminal, wrapped-ring expiry and cross-instance attach.
- `task lint`, `task test`, `task test:race`, `task spec` and `task api:check` are green, uncached for the repaired packages.
- The conformance suite certifies the shipped native runtime, the three `chains` scenarios assert their full contract, and the gate tools and the runnable examples run in CI.
- `README.md`, `docs/overview.md`, `docs/design/scenarios.md`, `CHANGELOG.md` and `docs/design/api-review-m0-5.md` state what the tree does, and the changelog lists every observed compatibility break with its replacement.

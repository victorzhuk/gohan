# Design

## Context

`openspec/specs/release/spec.md` freezes run mode, the shadow suppression sentinel and release identity. ADR-0096 records the intent: `RunInfo` mode, release and variant travel to spans, audit, `Done` and metrics. What exists today: `RunMode`, `RunInfo.ReleaseID`/`Variant`/`Mode`, `stores.Run.Mode`, `stores.Journal.ByFingerprint`, `types.StopShadowSuspended` and `ToolFinished.Replayed`. What is missing: every value above is unpopulated and no driver enforces the shadow rules.

## Goals / Non-Goals

Goals:

- One enforcement point per frozen rule: identity, appends, tool calls, suspension, telemetry.
- Shadow runs observable end to end through their own session, run row, span and events.
- Release identity visible on the four surfaces the scenario names.

Non-Goals:

- Binding `std/evals.Side` to a shadow conversation.
- A public `agent.VariantFlag` accessor; the frozen-flag path is the `variantFlag` argument.
- Shadow enforcement for foreign runtimes.
- Traffic splitting or promotion.

## Decisions

1. **Shadow is a stack build option.** `WithShadowMode()` and `WithReleaseIdentity(releaseID, variant, variantFlag)` are `Option`s stored on `Stack` and copied by `bindStack` into every conversation, so a shadow stack is single-purpose and a champion stack is untouched. An explicit opt-in preserves the invariant that ambient run identity never survives `Send`.
2. **The shadow session is a derived id.** `Send` mints the run id before `Runs.Start`, so the shadow row uses `<sessionID>/shadow/<runID>` while `RunInfo.SessionID` stays the primary session. Journal keys, history loads, cache keys and evaluation references therefore continue to name the primary session; only writes are redirected. The human-control handoff branch is skipped for a shadow conversation: that branch appends to the primary session to preserve an operator's thread, and a shadow run must not write it.
3. **Suppression lives at the batch execution seam.** The runtime batch calls one `Exec` for every registered tool, after the gate. Wrapping that seam covers read-only execution, journal replay and suppression in one place and leaves the gate free to suspend. Provider-executed settlements are the second site: their provider result is already spent, so the settle path applies the same replay-or-suppress mapping and writes no journal entry.

   A shadow run owes no ordinary approval: rule 2 says a call the journal cannot answer returns `ShadowSuppressed` and the model continues, so asking for permission to execute a call that will not execute would only block the challenger. The gate therefore keeps the live-control and taint decisions — an explicit deny still denies and an explicit ask still suspends, which the terminal suspension rule turns into `Done{Reason: ShadowSuspended}` — and settles everything else through replay or suppression.
4. **Replay matches the frozen rule literally.** A shadow call replays only a completed entry with the same `CallKey` and a fingerprint equal to the shadow call's fingerprint; `ByFingerprint` narrows, then `Key` and `State` decide. A miss suppresses.
5. **Suspension is handled once, in the lifecycle.** Every suspension the driver can raise converges on `Lifecycle.drive`, so a shadow branch there terminates the run with `Done{Reason: ShadowSuspended}` without `Runs.Suspend` and without a checkpoint write. Handling each `SuspendError` site separately would leave a site unprotected when a new one lands.
6. **A shadow run is a reader.** It loads primary history, skips model-step and provider journaling, skips the run-start audit, the title recipe, consolidation, online evaluation and compaction, and writes appends only to the shadow session. Those writes are the ones the release spec forbids; skipping them is cheaper and safer than redirecting each one.
7. **`Done` metadata is the event meta.** The streams contract keeps payloads bare and pairs every payload with `EventMeta`; `EventMeta` therefore grows `ReleaseID`, `Variant` and `Mode` (a value type, additive growth under `docs/design/compatibility.md`), populated by `RunMeta` and by the conversation relay from the run's `RunInfo`. `Mode` is what makes a stored shadow event carry `mode=shadow`; spans carry the same three attributes.
8. **Audit grows additively.** `stores.AuditRecord` gains `ReleaseID` and `Variant`, populated on `run.started` when the flow has a frozen flag snapshot. The hash chain appends a release-identity segment only when a record carries one, so an identity-less record keeps its historical seal bytes while a new record covers the identity it carries.
9. **Metric attributes travel with the run.** A decorator installed in `DriveLifecycle` stamps release and variant onto every emission the run makes through the ctx telemetry port; the cost ledger emits through its own port, so `recordCost` adds the same two attributes from the ctx `RunInfo`. Each run already owns its ledger and tree spend, so a shadow run's cost never reaches the primary run's tree accounting; the `"/shadow"` feature suffix is what attributes it.
10. **Variant resolution order.** A named frozen flag wins over the literal when the evaluated snapshot holds it; otherwise the literal applies. Nothing is re-evaluated per turn.

## Risks / Trade-offs

- Shadow mode refuses the non-native constructors: `NewConversation` and `NewForeignConversation` return an error on a shadow stack, so a runtime the driver cannot enforce can never be configured as a shadow challenger. Foreign shadow support is left to a later change.
- Suppression is a `Failed` tool result, so a candidate whose prompts depend on write results scores lower in shadow than in production; that is the frozen semantics and the reason `ToolFinished.Replayed` distinguishes replays.
- A sub-flow invoked as a tool inherits the shadow mode through the child run info, so its own tool calls are enforced; its child session and run rows are nonetheless filed under `<session>/<childID>`, not under `<session>/shadow/<runID>`. Namespacing child work under the shadow session is left to a later change.
- Two value structs grow (`EventMeta`, `stores.AuditRecord`). Both are keyed value structs, not ports, and `docs/design/compatibility.md` permits additive growth whose zero value preserves existing behavior.

## Migration Plan

Additive only. No stored shape migration: `EventMeta` and `AuditRecord` gain optional fields that older readers ignore. The six scenario ids leave the M3 deferred list and enter the M0.5 gate.

## Compliance

This change implements `release` requirements without editing the spec: shadow mode rules 1-5, the `ShadowSuppressed` sentinel, and release identity on spans, audit, `Done` metadata and metrics. No frozen port method changes.

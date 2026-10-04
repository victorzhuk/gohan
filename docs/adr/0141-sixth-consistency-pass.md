# 0141. Sixth consistency pass: the error catalog closes in both directions, and event meta travels beside its payload

Status: accepted

## Context

Row 3 of `openspec/changes/m0-core` lands the event vocabulary and the error catalog, and three contradictions stopped it.

The catalog requirement says every sentinel a spec defines has a row and `spec:types` fails when one does not. Read the other way, the catalog cited four sentinels that no spec declares — `ErrNotSuspendable`, `ErrVersionConflict`, `ErrToolDescription`, `ErrStructuredOutput` — while scenarios assert all four (`flow.not-suspendable`, `stores.optimistic-append`, `tools.description-injection`, `structured-output.validate-and-repair`). Six declared typed errors had no row, and `gohan.guard_blocked` cited `GuardBlocked`, which is the event, not the error.

`streams` said "all events embed `EventMeta{…}`" and then declared fourteen payload structs that carry no meta, so an event value alone held no `Seq` and no run identity.

## Decision

1. The four cited-but-undeclared sentinels are declared where their capability owns them: `ErrNotSuspendable` with the suspension sentinels, `ErrVersionConflict` with the store sentinels, `ErrToolDescription` with the tool sentinels, `ErrStructuredOutput` in the structured-output contract.
2. Three typed errors gain rows, so the catalog is closed in both directions: `gohan.chain_step` for `StepError`, `gohan.subflow_partial` for `PartialError`, `gohan.outcome_unknown` for `UncertainOutcomeError`. The last is `Retryable` at 409, because a run that finished with journal entries in `Outcome: Unknown` reconciles when the client retries with the same idempotency key.
3. Two typed errors are waived, and the requirement names them rather than leaving the exception implicit: `*SuspendError` is the suspension signal and `ToolError` is tool-result data the model reads. Neither reaches a transport, so neither can become a `Problem`.
4. `gohan.guard_blocked` cites `GuardBlockedError`. Its `Stage` field is what the row already says the code carries.
5. `Detail`'s allow-listed fields gain `key_id`: the `gohan.provider_key_rejected` row carries it, and a key id is an identifier, never the secret.
6. Event payloads stay bare. The harness pairs a payload with its `EventMeta` when it appends the event and when it delivers it, which is what the SSE `id:` field and `Done.Seq` need; embedding meta in fourteen structs would put run identity inside values that the block model and the store rows treat as data.

## Consequences

The catalog is closed in both directions, so the checks chunk 3.2 adds to `spec:types` can be green: every sentinel and typed error has a row (minus the two named waivers), no row's Source names nothing, and no Source names an undeclared error. `docs/design/types.md` is regenerated and reports 32 sentinels.

Two consequences follow for later rows: `ProblemOf` matches the innermost known error first, so a `StepError` wrapping a `ModelError` still reports the model code; and the transport that emits an event owns pairing it with its meta, which is where the `Seq` in `streams.monotonic-seq` is read from.

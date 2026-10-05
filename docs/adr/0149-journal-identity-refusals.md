# 0149. Journal identity refusals

Status: accepted

## Context

The journal is the identity seam of the exactly-once guarantee: a call key pins one intended effect, and a fingerprint is the hash of the tool name and its canonical arguments. Two gaps let that guarantee be bypassed.

- `MemoryJournal.Reserve` returned the stored entry on a call-key hit without comparing fingerprints, so a caller reusing a call key with different arguments inherited the earlier call's pinned outcome — including a `Completed` result from a different effect.
- `std.Journal` (the tool middleware) executed the tool when `Reserve` returned an error and discarded the error from `Complete`. A refused identity therefore still ran the effect, and a completion that never reached the store left the run reporting clean over an unrecorded write. A write that outlives its journal entry — the TTL is 24 h by default — reaches exactly that state.

## Decision

Both refusals are explicit, and the decorator stops discarding them.

- `Reserve` refuses a key presented with a fingerprint that differs from the stored entry's, with `ErrJournalFingerprintMismatch`; the stored entry is not modified.
- `Complete` refuses a key with no live reservation — expired or never reserved — with `ErrJournalCompleteMissed`.
- `std.Journal` fails the call when `Reserve` errors, and marks the result `Outcome: Unknown` when `Complete` errors, so the run ends with the uncertainty the harness already reports (`*UncertainOutcomeError`) rather than a clean result.

Both sentinels are store-level errors declared beside the ports, as `ErrRunNotFound` and `ErrOutputNotFound` are; they are not part of the typed-error catalog in `core/types`.

## Consequences

- The two refusals are covered by the store conformance suite, so every `Journal` implementation — the memory store today, Postgres later — is held to them.
- A side-effecting call can no longer execute under an identity the store refused, and a lost completion surfaces as uncertainty instead of a silent clean run.
- The journal TTL becomes a behavioural boundary rather than a bookkeeping detail: a tool call that outlives its reservation now reports uncertainty.
- `testkit/storetest` gains `stores.reserve-fingerprint-mismatch` and `stores.complete-without-reservation`, registered in `openspec/scenarios.json` with this ADR as their origin.

## Alternatives

- Leaving the key-hit comparison to the caller. Rejected: the caller cannot see the stored fingerprint, and a decorator that inherits keys (`ByFingerprint`) is exactly the component that must not be trusted to have got it right.
- Keeping the decorator's fail-open path. Rejected: it converts exactly-once into at-most-once for every side-effecting tool, silently.
- Treating a completion miss as a hard call failure. Rejected: the effect already happened, so failing the call invites a retry that would double it; uncertainty is the accurate report.

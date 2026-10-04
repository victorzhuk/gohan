# ADR-0108: `iter.Seq2[T, error]` stays; error-tuple protocol and helpers

Status: accepted · Amended by ADR-0116 (buffered provider read). · Origin: grill round 34 (2026-09-30)

## Decision

Every streaming seam keeps `iter.Seq2[T, error]`. `streams` defines the protocol all of them follow: an error tuple carries the zero value, is terminal, and is never followed by another `yield`; pre-flight failures arrive as the sole tuple; cancellation surfaces as a final `context.Canceled` tuple after the safe point; early break loses nothing already known. Core ships `Collect`, `Last`, `Drain`. Core and `std` never call `iter.Pull` on the request path. `Flow.Invoke` stays `(Out, error)`.

## Context and evidence

The community critique of `Seq2[T, error]` is real (species fragmentation, pre-flight errors, partial consumption) but the callback alternative cannot interleave mid-stream errors with events, which gohan's guard/fallback/`Done` sequence needs. Pinning the protocol and giving non-streaming callers helpers addresses the critique without churn.

## Consequences

Three `streams` scenarios; `model` iterator contract and `flow` reference the protocol; task 26 updated.

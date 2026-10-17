# 0170. The research report reaches native Resume and reports its own ledger

Status: accepted

## Context

`examples/research-report` composed deferred discovery, notes, compaction, detached execution and client reattachment, but it never recovered from a real suspension. It reconstructed a session log by hand, seeded records into it and continued from that reconstruction, so the run it reported was not a run the harness had suspended and restarted. The example also described its retrieval batch as a tree of child runs with a parent total, and its wide-fanout report summed the same fixture cost buckets it returned as per-child costs. Both claims described an accounting shape the example does not have.

The fixture sources are read-only retrievals executed as sequential model turns inside one run. There are no child runs. The harness ledger is per run, and the shipped `RunLimits` surface reports a typed `*types.LimitExceededError` when a run crosses its ceiling.

## Decision

The example resumes through the shipped native path. The composed flow is two runs over one durable history: a completed `Send`, a `Continue` that loads that durable history, publishes the compaction and suspends for `AwaitingControl` with a nonempty checkpoint token, and one `conv.Resume(ctx, token, stores.ResumeInput{Verdict: stores.VerdictApprove})` in the same conversation over that token. The checkpoint store is `stores.NewMemoryCheckpoints()` bound through `gohan.WithConversationCheckpoints`. Reconstruction by hand is removed: no hand-built session log, no seeded records, no manual compaction append, no replacement stack.

The recovery claimed is that same-conversation `Resume` and nothing wider. Another-pod `Replay` is not claimed. Harness activation restoration is not claimed: the source definitions the resumed request carries are recomputed by the flow's own assembler over the reloaded history, and `tools.activation-persists-across-resume` and `tools.governed-while-deferred` stay unmet, named and skipped.

The reporting boundary is narrow. The report carries the suspended run's actual terminal record, that run's persisted records after the suspension sequence, whether the token is consumed after the resume, and the provider-entry requests the fixture wrapper captured. Provider captures are frozen at the suspension boundary so the first resumed capture can be compared with the original capture at that boundary. Costs are observed per run from the harness's own completed run records and compared against independently declared expected values. A missing token, a wrong terminal record, an unbound checkpoint, a missing resumed suffix, an unconsumed token or an absent refusal is an error, never an alternate reconstruction.

The upstream parent-plus-two-children run-tree requirement stays unchanged and is deliberately unproved by this example. Its own core test remains its owner.

## Consequences

The example now demonstrates that a durable compaction, a genuine `AwaitingControl` suspension and a token-based `Resume` compose in one offline run, and that the provider request the resumed run assembles matches the one the original assembled at the suspension boundary. Cost evidence becomes independent: the harness records the charge, the test declares the expectation, and no source-derived bucket feeds the report.

This ADR does not amend core semantics. No core, `std` or adapter contract changes with it, and no requirement outside the example's own `context` requirement block changes. The activation gaps and the `std/flow` recipe prerequisites recorded against this example stay unmet.
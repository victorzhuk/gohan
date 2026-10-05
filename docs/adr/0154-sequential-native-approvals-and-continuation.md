# 0154. Sequential native approvals and the durable continuation record

Status: accepted

## Context

The `runtime` spec requires a batch's asks to suspend one at a time in call order with a single `ApprovalRequest`, and the complete result set to carry exactly one result per call in call order. The native path does neither. It stores every ask in `State.Pending` and reports `AwaitingBatch` instead of `HumanApproval`, so `Resume` refuses `Approve()` and `Deliver` settles the call without executing it. `approvalsFor` builds one approval per pending call, and `Resume` applies a verdict to all of them at once. The settled results of a mixed batch are dropped before the checkpoint, so a fresh conversation resumes with unanswered calls. And the native phase replaces `State.Backend` wholesale, so there is nowhere for a continuation to carry the batch.

Repairing this needs a durable continuation representation, which changes the private checkpoint format: a native approval checkpoint must carry the original batch, its settled and unresolved results, and the run's admitted tool-call total, and a decision must apply to one ask only. Without that, a resumed approval either loses the reservation accounting or re-authorizes asks nobody approved.

## Decision

1. `State.Pending` is the durable ask queue in original call order, and `Pending[0]` alone is the active ask. A native approval checkpoint carries exactly one approval, matching `Pending[0]` by id, tool and arguments.
2. The native backend carries a private progress record: a version, the admitted tool-call total, and a batch continuation holding the original calls, an aligned result-slot array and a `Ready` flag. Phase and driver record are separate fields; advancing the phase preserves the driver bytes the effect produced. `Ready` permits an attempt to settle the active ask after quorum and never bypasses a current hard block or live deny.
3. A decision settles or mutates exactly one ask. Approval and rejection settle the active slot and remove only that ask; edit changes only the active call's arguments, clears only its votes and advances the generation once; the remaining asks stay durable and each suspends with its own token. No model call and no new reservation occur between asks.
4. Continuation entries are prepaid. A fresh batch reserves every original call once against `MaxToolCalls`; continuation execution consumes an already-paid slot instead of reserving a new one, so approvals, rejections, edits and quorum votes change the admitted total by zero. A reduced configured maximum blocks new batches, not already-paid asks.
5. The checkpoint envelope version becomes 2. Version 1 native approval checkpoints and multi-call version 1 approval envelopes refuse with `ErrCheckpointIncompatible` before the token is consumed: their recorded votes cannot prove one-at-a-time approval, and their reservation and settled-result evidence is absent, so honouring them would fabricate both. Version 1 foreign single-ask approval envelopes remain resumable and are normalized on the next write. The legacy raw-state non-approval fallback is unchanged, and no raw-state fallback follows a malformed envelope. The store schema version and every public checkpoint field are unchanged.
6. Approval receipts get their own version constant instead of reusing the envelope version, so a trusted receipt written under version 1 stays valid under version 2 envelopes.
7. Settled results are durable before a suspension is acknowledged, and final settlement appends only results not already present, in original relative order.

## Consequences

- Approvals are per ask, as the spec requires, at the cost of one suspension round per ask.
- Old native approval tokens refuse instead of resuming with fabricated authority or accounting; the incompatibility is recorded in the changelog.
- The native phase format carries data across effects, so a continuation survives suspension, resume and crash recovery.

## Alternatives

- Keeping one checkpoint-wide approval round. Rejected: it contradicts the `runtime` spec's one-at-a-time rule, and a session grant recorded for one call would silently authorize an unapproved ask with a matching fingerprint.
- Adding public `State` fields for the continuation. Rejected: `runtime.State` and the store ports are the frozen v1-candidate surface; a private record in `Backend` needs no port change.
- Reconstructing the continuation from the session log alone. Rejected: read-only results are not journaled, so a lost settled result would be unrecoverable and the batch would re-execute or misreport.
- Migrating version 1 native approval tokens automatically. Rejected: their reservation total and settled results cannot be recovered from the checkpoint bytes, so any migration would invent evidence.

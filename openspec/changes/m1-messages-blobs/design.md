# Design: m1-messages-blobs

## Context

See `proposal.md` — Why. The behaviour is already normative in the capability specs; this design decides the seams that connect it to the live tree. Read against the tree on 2026-10-07:

- **The port is declared, not built.** `BlobUploader` is normative in `model` (openspec/specs/model/spec.md:190-192) but has no Go declaration: the type floor holds `Blob`, `Image`, `Audio` and `File` (`core/types/message.go:74-100`) and `BlobCaps` (`core/types/model_profile.go:69`), and nothing else. `types.Blob` is `{Ref, SHA256, Bytes}` — no provider identity slot.
- **Bytes are stored once and read by ref.** `InlineBlobBytes` is the 64 KiB spill threshold and `OutputStore` is content-addressed (`core/stores/outputs.go:16-30`); assembly today always inlines bytes, so a 3 MB image is re-read and re-encoded every turn.
- **The provider-identity pattern already works for `Raw`.** `Raw{Provider, Value}` round-trips to its own adapter and is dropped elsewhere (`core/provider_result.go:29-33`; adapter/openai/request.go:72-77; adapter/anthropic/request.go:160-163). The existing round-trip tests are `TestRetryAfterPropagationAndRawFidelity` (adapter/openai/send_test.go:386) and the `raw-fidelity` subtest (adapter/anthropic/send_test.go:202), and the shared suite checks it with `assertRawFixture` (testkit/conformance/model.go).
- **Session metadata has an optional surface.** `stores.SessionStateMeta` (`SharedStateMeta`/`SetSharedStateMeta`, core/stores/session.go:118-125) shows the pattern: an optional surface on a `SessionLog` that survives a resume on another pod. The blob ledger needs the same durability.
- **No scenario test exists.** `core/stores/blob_checks_test.go` covers the caps scenarios and `core/stores/outputs_test.go` covers `messages.blob-stored-by-ref`; nothing covers `messages.blob-provider-id-reused` (openspec/changes/m1/design.md:178).

## Goals / Non-Goals

**Goals:** carry the issuing provider and its id with the blob, persist a `ref → provider id` ledger in session metadata, upload once per profile, and turn `messages.blob-provider-id-reused` green on the public path for both adapters through the shared suite.

**Non-Goals:** This change adds no second blob store and no new transport module. It does not change `InlineBlobBytes`, `BlobCaps` or the `ErrBlobTooLarge` limits path (`core/stores/blob_checks.go`). It does not add a blob kind, a new `BlockKind` value or a new error sentinel. It does not re-implement the `Raw` round-trip. Adapter wire encodings stay as they are; only the id substitution is added.

## Decisions

### D1 — The provider id is carried on the block and persisted in session metadata

Two carriers, one identity. The durable ledger is session metadata: an optional `stores.SessionBlobs` surface on the `SessionLog`, mirroring `SessionStateMeta`, with `BlobProviderID(ctx, sessionID, ref) (provider, id string, ok bool)` and `SetBlobProviderID(ctx, sessionID, ref, provider, id string) error`, plus a memory implementation. The ledger is keyed by `Ref` because storage is content-addressed and the same bytes may be attached to more than one message. The wire carrier is the block: `types.Blob` gains `Provider string` and `ProviderID string`, both empty until an upload resolves them, so the adapter reads the id from the block it is already encoding and no side channel is added to `ModelRequest`. This mirrors `Raw{Provider, Value}` (D-Context), where the provider name lives on the block rather than in a side map. Rationale: the durable record must outlive the turn that uploaded the blob (a resume on another pod reads it from the session row), while the wire form must be visible to the adapter; splitting the two is what the requirement means by "records `ref → provider id` in session metadata, and sends the id on later turns".

### D2 — A different provider is a ledger miss, never a re-tag

The ledger entry names the provider that issued the id. Assembly resolves a block against the target profile's name: a hit for the same provider sets `Blob.ProviderID` and leaves `Data` nil; a miss — including an entry issued by a different provider — starts a fresh upload when the target adapter implements `BlobUploader`, or loads the bytes from `OutputStore` and inlines them when it does not. An id issued by provider A is never handed to provider B, never rewritten to name B, and never sent as if B had issued it. A ref re-sent to a different provider therefore costs one more upload (or a byte load), not a wrong id. This is the same discipline as the `Raw` foreign-drop (adapter/anthropic/request.go:160-163), with re-materialisation instead of dropping because a blob carries content a `Raw` does not.

### D3 — Misuse is `ErrInputInvalid` (`gohan.input_invalid`, `Permanent`)

The only way a foreign or stale provider id reaches assembly is bad session metadata: a ledger row naming a provider other than the profile being assembled, or a row whose bytes are gone because the ref was erased. D2 covers the ordinary cross-provider case by re-materialising, so a genuine misuse is a ledger row that cannot be honoured and whose bytes cannot be recovered. Assembly rejects it before any request is built with `ErrInputInvalid`, which the closed catalog maps to `gohan.input_invalid` (`Permanent`, HTTP 422, core/types/problem.go:160). No new sentinel is added, so `spec:types` (`errors.every-sentinel-has-code`) is unaffected. Rationale: the error is permanent and non-retryable, and reusing an existing catalog row keeps the catalog closed; a bare `*types.ModelError{ClassPermanent}` would fall through `modelRow` to `gohan.internal` (core/types/problem.go:240-241), which would misreport a data problem as a harness bug.

### D4 — The conformance suite pins it for both adapters

`testkit/conformance` gains a blob fixture and one assertion so both adapters run the same behaviour under the registered scenario ID. The fixture carries an image larger than `InlineBlobBytes` and a model double whose `BlobUploader.Upload` counts calls and returns a stable id. `assertBlobProviderIDFixture` sends three turns: it requires exactly one `Upload` call across the three, requires turns two and three to carry the provider id in the outgoing body, and requires no re-upload. The test is registered by both adapters as a subtest named exactly `messages.blob-provider-id-reused`, the scenario ID. Rationale: the rule is an assembly-plus-adapter contract, so the suite must exercise the same path the `Raw` fixture does; a per-adapter test would fork the fixture shape.

### D5 — Upload happens at assembly, not at spill

Bytes are written to `OutputStore` when they enter the harness (`Send`, tool result, provider result), which is before a target profile is known. The upload therefore happens in native assembly, where the resolved profile and its adapter are known: the assembler consults the ledger, uploads through the adapter's `BlobUploader` when needed, writes the ledger row, and stamps `Blob.Provider`/`Blob.ProviderID` on the request's copy of the block. The persisted `SessionLog` block keeps `Data` nil and the ref (messages, *Blobs live once*), so no upload state is written into history. Rationale: spill has no profile, and the ledger must survive a resume, so the session-metadata write belongs to assembly.

## Risks / Trade-offs

- [Risk] A ledger write on every first upload adds a session-metadata round trip per blob. → Accepted: it is one write per `(session, ref)` and replaces a full byte load plus base64 encode on every later turn.
- [Risk] A `SessionLog` without the optional `SessionBlobs` surface cannot persist the id, so it re-uploads each turn. → Accepted and stated: the surface is optional like `SessionStateMeta`; a store without it falls back to re-upload, which is correct but slower, and `Build` does not fail for it.
- [Risk] Adding two fields to `types.Blob` changes a normative stored shape. → Accepted: both default to empty and are omitted from the persisted form (only the ref, hash and size persist), so a stored block and a request block stay compatible.

## Migration Plan

No data migration is required. The change adds `types.BlobUploader`, the two empty-by-default `Blob` fields, the optional `stores.SessionBlobs` surface with its memory implementation, one assembly step and one error path, plus the shared-suite fixture. The `messages` Contract `Blob` block (Contract 6.1) gains `Provider` and `ProviderID` in the same implementation commit and `docs/design/types.md` is regenerated; the package ships a `messages` delta declaring the edit. The `messages` main spec already carries the rule and the scenario, so the delta reproduces the *Blobs* requirement as a strict superset and adds the normative provider-id rule; the archive merge must union that rule and keep every existing scenario. Keep the module pre-v1 and create no release tag during M1 authoring (openspec/changes/m1/design.md:20-27). No adapter wire format changes beyond substituting the id for the bytes.

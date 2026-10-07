# Spec Delta

## Context

This delta MODIFIES the `messages` *Blobs* requirement. The main requirement block carries five scenario blocks and no normative prose. This delta is a strict superset of that block: it reproduces all five scenarios unchanged and adds the provider-id rule the requirement lacked. The archive merge MUST union the added rule and MUST keep every reproduced scenario.

The change also edits the `messages` Contract: the `Blob` struct (Contract 6.1, `openspec/specs/messages/spec.md`) gains `Provider string` and `ProviderID string`, both empty until an upload resolves them. The Contract block is edited in the same implementation commit and `docs/design/types.md` is regenerated, exactly as the egress and hedging packages declare their Contract edits. This delta reproduces the requirement the edit is written against; the Contract edit itself is normative and lands with the change.

## MODIFIED Requirements

### Requirement: Blobs

An `Image`, `Audio` or `File` whose `Data` exceeds `InlineBlobBytes` is stored once in `OutputStore` and persisted as `Blob{Ref, SHA256, Bytes}` with nil `Data`; storage is content-addressed. Assembly loads bytes from the store only when the adapter has no provider handle. An adapter implementing the optional `BlobUploader` (`model` *Provider adapter contract*) uploads a blob once per profile, records `ref → provider id` in session metadata, and sends the provider id on later turns.

The provider id carries the identity of the provider that issued it: `Blob` MUST carry the issuing provider name and its id, and assembly MUST send the id only to that provider. A blob re-sent to a different provider MUST NOT reuse, rewrite or re-tag the id — it MUST be re-uploaded for that provider, or its bytes loaded from `OutputStore` and inlined when the target adapter has no `BlobUploader`. A ledger row that cannot be honoured and whose bytes are gone MUST fail with `ErrInputInvalid` before any request is built.

#### Scenario: blob stored by reference
ID: `messages.blob-stored-by-ref`
- WHEN `Send` carries a 3 MB `Image`
- THEN `SessionLog` holds the block with `Blob{Ref, SHA256, Bytes}` and nil `Data`, and `OutputStore.Get(Ref)` returns the bytes

#### Scenario: blob content-addressed
ID: `messages.blob-content-addressed`
- WHEN the same image is sent in two messages
- THEN both blocks carry the same `Ref` and the store holds one copy

#### Scenario: provider id reused
ID: `messages.blob-provider-id-reused`
- WHEN the adapter implements `BlobUploader` and a conversation runs three turns after an image was attached
- THEN the image bytes are uploaded once and turns two and three send the provider id

#### Scenario: url only from user
ID: `messages.url-only-from-user`
- WHEN a tool result contains an `Image` with a `URL`
- THEN the harness fetches it under the egress policy and stores a blob, and the request to the provider carries no URL

#### Scenario: blob too large
ID: `messages.blob-too-large`
- WHEN `Send` carries a `File` over `Caps.Blobs.MaxBytes`
- THEN `ErrBlobTooLarge` is returned before any store write and no run starts

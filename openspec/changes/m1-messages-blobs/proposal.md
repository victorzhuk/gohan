# Proposal: m1-messages-blobs

## Why

The `messages` *Blobs* requirement declares that an adapter implementing the optional `BlobUploader` uploads a blob once per profile, records `ref → provider id` in session metadata, and sends the id on later turns (openspec/specs/messages/spec.md:213,286-309). The `model` contract already declares the port (`BlobUploader.Upload(ctx, b Blob, mime string) (string, error)`, openspec/specs/model/spec.md:190-192), and the `Raw` round-trip path shows the same provider-identity shape working today (`core/provider_result.go:29-33`; adapter/openai/request.go:72-77; adapter/anthropic/request.go:160-163). But no adapter exercises the blob rule: `BlobUploader` has no Go declaration, no upload ledger exists, and assembly always inlines bytes. The registered scenario is left `missing` for exactly that absent path (`messages` spec declares the rule; nothing verifies it, openspec/changes/m1/design.md:178), and the milestone record names this change as the decomposition target for the ID (`m1-s1-s2-acceptance` owns it; decompose it before implementation, openspec/changes/m1/design.md:224).

## What Changes

- Declare `types.BlobUploader` in the type floor and carry the issuing provider and its id on the assembled block (`Blob.Provider`, `Blob.ProviderID`), mirroring `Raw.Provider` and its value.
- Add a session blob ledger, written through an optional session-log metadata surface, that maps a blob `Ref` to the provider that issued an id for it.
- Resolve blobs in native assembly: a ref with a ledger hit for the target provider sends the id; a miss uploads through a `BlobUploader` adapter once per profile, records the id, and sends it on later turns; an adapter without the port receives base64 bytes from `OutputStore` as today.
- Pin the behaviour in `testkit/conformance` so both adapters run the same provider-id fixture under the registered scenario ID.
- Preserve and cover the one scenario this lane owns: `messages.blob-provider-id-reused`.

Non-breaking: this adds a path that has no implementation today; no shipped public behaviour is removed.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `messages`: the *Blobs* requirement carries the provider-id rule in the Contract and the `messages.blob-provider-id-reused` scenario, but the requirement block itself states no normative rule. The delta is a strict superset of the requirement: it reproduces all five scenario blocks and adds the provider-id rule the requirement lacked (openspec/specs/messages/spec.md:213,286-309).

## Impact

Affected: `core/types` (the `BlobUploader` port and the two `Blob` fields), `core/stores` (the optional session blob-ledger surface and its memory implementation), `core` (native assembly blob resolution and the misuse error), `adapter/openai` and `adapter/anthropic` (consume `Blob.ProviderID` in the request body), and `testkit/conformance` (the shared provider-id fixture). No third-party dependency, no new transport module and no adapter-internal redesign. The lane is a `core`/`adapter`/`testkit` change; the model capability's `BlobUploader` port is already normative, so no spec outside this package is edited.

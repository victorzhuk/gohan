# ADR-0119: Binary blocks are content-addressed blobs; URLs are never forwarded on the model's behalf

Status: accepted · Amended by ADR-0129 (egress client). · Origin: grill round 45 (2026-09-30)

## Decision

`Image`, `Audio` and `File` above `InlineBlobBytes` are stored once in `OutputStore` (content-addressed) and persisted as `Blob{Ref, SHA256, Bytes}`; deletion and erasure cascade. Adapters implementing the optional `BlobUploader` send a provider file id on later turns instead of bytes. A URL block is forwarded to a provider only when its origin is user or system; any other URL is fetched by the harness under the egress policy and stored. `Caps.Blobs` and `RunLimits.MaxBlobBytes` bound sizes, counts, formats and per-session totals; guards receive blobs as `GuardInput.Blobs`.

## Context and evidence

Provider limits (10 MB per image, 100 per request, automatic downscaling, visual-token formula) and Files APIs exist because base64 resends full bytes every turn; providers fetch URL sources server-side, which makes a model-written URL an exfiltration channel outside the harness's egress control. gohan's blocks carried inline bytes with no storage, size or origin rules.

## Consequences

`messages` v1.1 (`Blob`, `ErrBlobTooLarge`, three rules, five scenarios), `model` v1.3 (`BlobCaps`, `BlobUploader`), `limits` v1.5 (`MaxBlobBytes`), `build` v1.2, `redaction` v1.2, `guards` v1.1 (`GuardInput.Blobs`), one scenario each; task 12; uploader and erasure scenarios in M1.

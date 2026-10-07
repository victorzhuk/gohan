# Tasks

Delivered in `feat(messages): reuse provider blob ids across turns`. A task is done when its listed scenario ID passes and `task lint`, `task spec` and `task test` are green.

## 1. Blob port and provider identity

- [x] 1.1 Add `type BlobUploader interface { Upload(ctx context.Context, b Blob, mime string) (string, error) }` to `core/types/model_profile.go` beside `BlobCaps`, matching the normative port; verify `go test -short -timeout 2m ./core/types/... -run 'TestBlobUploaderShape'`.
  - evidence: `core/types/model_profile.go` (`BlobUploader`); `core/types/model_profile_test.go` (`TestBlobUploaderShape`)
- [x] 1.2 Add `Provider string` and `ProviderID string` to `types.Blob` (`core/types/message.go:74`), both empty until an upload resolves them, and assert the persisted form still carries only `Ref`, `SHA256`, `Bytes`; verify `go test -short -timeout 2m ./core/types/... -run 'TestBlobProviderFields'`.
  - evidence: `core/types/message.go` (`Blob.Provider`, `Blob.ProviderID`); `core/types/message_test.go` (`TestBlobProviderFields`)

## 2. Session blob ledger

- [x] 2.1 Add the optional `stores.SessionBlobs` surface on `SessionLog` — `BlobProviderID(ctx, sessionID, ref) (provider, id string, ok bool)` and `SetBlobProviderID(ctx, sessionID, ref, provider, id string) error` — mirroring `SessionStateMeta` (`core/stores/session.go:118-125`); verify `go test -short -timeout 2m ./core/stores/... -run 'TestSessionBlobLedger'`.
  - evidence: `core/stores/session.go` (`SessionBlobs`); `core/stores/session_blobs_test.go` (`TestSessionBlobLedger`)
- [x] 2.2 Implement the surface on `MemorySessionLog` with a `ref → {provider, id}` map that survives `Load` and a resume on a rebuilt store (the ledger is persisted through `StoredRecord`/`SeedRecord` and read back), and assert a store without the surface is detected by type assertion; verify `go test -short -timeout 2m ./core/stores/... -run 'TestSessionBlobLedger'`.
  - evidence: `core/stores/session_memory.go` (`BlobProviderID`, `SetBlobProviderID`); `core/stores/session_blobs_test.go` (`TestSessionBlobLedgerMemory`, `TestSessionBlobLedgerRebuild`)

## 3. Assembly resolution

- [x] 3.1 Resolve blob blocks in native assembly: a ledger hit for the target profile sets `Blob.ProviderID` and `Blob.Provider` and leaves `Data` nil; a miss uploads through the adapter's `BlobUploader` once per `(session, ref)`, writes the ledger row and sends the id; an adapter without the port loads bytes from `OutputStore` and inlines them as today; verify `go test -short -timeout 2m ./core/... -run 'TestBlobAssemblyResolution'`.
  - evidence: `core/native_assemble_blobs.go` (resolution step); `core/native_assemble_blobs_test.go` (`TestBlobAssemblyResolution`)
- [x] 3.2 Treat a ledger row naming a different provider as a miss, never a re-tag: the block re-materialises for the target provider and no foreign id reaches the request; verify `go test -short -timeout 2m ./core/... -run 'TestBlobForeignProviderRematerialises'`.
  - evidence: `core/native_assemble_blobs.go`; `core/native_assemble_blobs_test.go` (`TestBlobForeignProviderRematerialises`)
- [x] 3.3 Reject a ledger row that cannot be honoured and whose bytes are gone with `ErrInputInvalid` before any request is built, mapping to `gohan.input_invalid` (`Permanent`, 422); verify `go test -short -timeout 2m ./core/... -run 'TestBlobMisuseInputInvalid'` and assert `types.ProblemOf(err).Code == "gohan.input_invalid"`.
  - evidence: `core/native_assemble_blobs.go`; `core/native_assemble_blobs_test.go` (`TestBlobMisuseInputInvalid`)

## 4. Adapter consumption and shared suite

- [x] 4.1 Encode `Blob.ProviderID` in the OpenAI request body in place of base64 bytes when the id is set, and fall back to bytes when it is not; verify `go test -short -timeout 2m ./adapter/openai/... -run 'TestBlobProviderID'`.
  - evidence: `adapter/openai/request.go` (id substitution); `adapter/openai/send_test.go` (`TestBlobProviderID`)
- [x] 4.2 Encode `Blob.ProviderID` in the Anthropic request body in place of base64 bytes when the id is set, and fall back to bytes when it is not; verify `go test -short -timeout 2m ./adapter/anthropic/... -run 'TestBlobProviderID'`.
  - evidence: `adapter/anthropic/request.go` (id substitution); `adapter/anthropic/send_test.go` (`TestBlobProviderID`)
- [x] 4.3 Add the shared blob fixture and `assertBlobProviderIDFixture` to `testkit/conformance`: three turns after an image above `InlineBlobBytes` is attached, the inline turn streams its chunks and turns two and three carry the provider id with no bytes. The model suite has no assembly seam and its doubles do not upload, so the "exactly one `Upload` call" half is proven on the shipped path; verify `go test -short -timeout 2m ./testkit/conformance/... -run 'TestModelConformance|TestBlobProviderIDReuseShippedPath'`.
  - evidence: `testkit/conformance/model.go` (`assertBlobProviderIDFixture`); `testkit/conformance/fixtures.go`; `testkit/conformance/blob_provider_id_test.go` (`TestBlobProviderIDReuseShippedPath`, one upload across three turns)

## 5. Scenario behavior and package gates

- [x] 5.1 Register a top-level `TestBlobProviderID` whose subtest is named exactly `messages.blob-provider-id-reused`, driving the shipped outbox, the real assembler and a `BlobUploader` adapter double through the public path; verify `go test -short -timeout 2m ./core/... -run 'TestBlobProviderID'`.
  - evidence: `core/blob_provider_id_test.go` (`TestBlobProviderID` with the ID-named subtest)
- [x] 5.2 Run the repository wrappers and resolve failures; verify `task lint` and `task test`.
  - evidence: driver-run `task lint` 0 issues; `task test` green across root, `adapter/openai`, `adapter/anthropic`, `testkit`
- [x] 5.3 Validate the change after all required M1 owners land; verify `openspec validate m1-messages-blobs --strict --no-interactive` and `task spec:gate GATE=M1`.
  - evidence: driver-run `task spec:gate GATE=M1` with `messages.blob-provider-id-reused` no longer `missing`

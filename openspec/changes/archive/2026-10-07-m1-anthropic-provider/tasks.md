# Tasks

## 1. Authoring

- [x] 1.1 Record the eight registered IDs, the module layout to mirror, and the adapter-specific mapping. Evidence: `design.md` Context, D1, D5.
- [x] 1.2 Add the `model` delta with one added requirement and ID-less scenarios. Evidence: `specs/model/spec.md`.
- [x] 1.3 Validate this package. Verify: `openspec validate m1-anthropic-provider --strict --no-interactive`.
- [x] 1.4 Record the dependencies on `m1-provider-metadata-contract` and `m1-provider-conformance-contract`. Evidence: `design.md` D8.

## 2. Module scaffold

- [x] 2.1 Create `adapter/anthropic/go.mod` (`module github.com/victorzhuk/gohan/adapter/anthropic`, `go 1.27.1`, require `github.com/victorzhuk/gohan v0.1.0`) and `adapter/anthropic/anthropic.go` with package `anthropic`, `Config`, `New`, and `Model` implementing `types.Model` (`Profile()`, `Generate`). Verify: `go -C adapter/anthropic build ./...`.
  - evidence: `adapter/anthropic/go.mod`, `adapter/anthropic/anthropic.go`.
- [x] 2.2 Add `./adapter/anthropic` to `go.work` and the module to the `test`, `test:race`, `spec` and `spec:gate` command lists in `Taskfile.yml`. Verify: `task test`.
  - evidence: `./adapter/anthropic` in `go.work`; `Taskfile.yml` carries `go -C adapter/anthropic test` lines.
- [x] 2.3 Implement request translation and the per-request governed-key collision check on `ModelOptions.Extra`, returning `ClassPermanent` before any request. Verify: `go -C adapter/anthropic test -timeout 2m -short -run 'TestAnthropicSend/extra-collision'`.
  - evidence: `adapter/anthropic/request.go` collision check; `send_test.go` subtest `extra-collision`.
- [x] 2.4 Implement error normalization through `types.ClassifyProviderError`, `Retry-After` parsing and `ClassVersionDrift` on strict-version mismatch. Verify: `go -C adapter/anthropic test -timeout 2m -short -run 'TestAnthropicSend/retry-after'`.
  - evidence: `adapter/anthropic/errors.go`; `send_test.go` subtests `retry-after`, `version-drift`.

## 3. Adapter conformance obligations (suite tasks 3.2 and 3.3)

- [x] 3.1 Record Anthropic wire fixtures for the shared set and a fixture-backed `types.Model` decoding them through the real path. Verify: `go -C adapter/anthropic test -timeout 2m -short -run 'TestModelConformance'`.
  - evidence: `adapter/anthropic/fixtures.go`; `conformance_test.go` `TestModelConformance`.
- [x] 3.2 Run the suite so the eight registered cases pass as subtests: `model.status-to-class`, `model.retry-after-parsed`, `model.cache-explicit-breakpoints`, `model.schema-never-ignored`, `model.usage-estimated`, `model.raw-round-trip`, `model.isolation-per-key`, `model.key-never-in-telemetry`. Verify: `go -C adapter/anthropic test -timeout 2m -short -run 'TestModelConformance' -v`.
  - evidence: `adapter/anthropic/conformance_test.go` `TestModelConformance` runs the eight registered cases.
- [x] 3.3 Map `CacheBreak` to ordered `cache_control` markers capped by `Caps.CacheBreakpoints`; honour `ResponseSchema` through a forced tool when `Caps.Constrained`, else refuse before send. Verify: `go -C adapter/anthropic test -timeout 2m -short -run 'TestAnthropicSend/(cache|schema)'`.
  - evidence: `adapter/anthropic/request.go` `cache_control` markers + `forcedTool`; `send_test.go` subtests `cache-markers`, `schema-refused`, `schema-forced-tool`.
- [x] 3.4 Round-trip opaque `Reasoning` signatures and pass `Raw` blocks through unchanged to the same provider. Verify: `go -C adapter/anthropic test -timeout 2m -short -run 'TestAnthropicSend/(raw|reasoning)'`.
  - evidence: `send_test.go` subtests `raw-fidelity`, `reasoning-signature-round-trip`.

## 4. Public Send proofs

- [x] 4.1 Prove key secrecy: a sentinel credential is absent from span attributes, logs, error strings and the recorded cassette, while `KeyModelKeyID` names the credential. Verify: `go -C adapter/anthropic test -timeout 2m -short -run 'TestAnthropicSend/key-secrecy'`.
  - evidence: `send_test.go` subtest `key-secrecy`.
- [x] 4.2 Prove per-key isolation: a rate-limited key for one tenant does not affect another tenant's calls on the same profile. Verify: `go -C adapter/anthropic test -timeout 2m -short -run 'TestAnthropicSend/isolation-per-key'`.
  - evidence: `send_test.go` subtest `isolation-per-key`.

## 5. Repository gates

- [x] 5.1 Run the repository gates after implementation. Verify: `task lint && task test && task spec && task api:check`.
  - evidence: driver ran `task lint` (0 issues) and `task test` green across root, `adapter/otel`, `adapter/openai`, `adapter/anthropic`, `examples`.

# Tasks

## Authoring

- [x] 1.1 Assign the six M1 scenario IDs and preserve separate subject-erasure ownership.
  - evidence: `openspec/changes/m1/design.md:115-128`; package delta lists only the five redaction IDs and `guards.redaction`.
- [x] 1.2 Map the current assembly, output, store, and Build seams. Record absent policy registration, map persistence, and store-region contracts as blockers.
  - evidence: initial design survey; superseded by ADR-0157 decisions 1-3 and current design D2-D6.
- [x] 1.3 Define deterministic public-path test seeding and existing-service-strict evidence requirements.
  - evidence: design public-path test seeding; tasks 2-6 require store interception and public Build/Send/Resume tests.
- [x] 1.4 Implement the package-owned ADR-0157 seams: declare redaction contracts in `core/types`, implement `types.Redactor` in `std/redact`, add `WithRedactor(types.Redactor) Option` to native config resolution, and add tenant/session-keyed `Stores.RedactionMaps` Put/Get integration without checkpoint storage.
  - acceptance: no second builder or `NativeSpec.Redactor`; resolved native flow carries the option through Build, Send, and Resume; map storage is independent of checkpoints and journal entries.
  - verify: `go test -timeout 10m ./core -run 'Test.*Redactor.*(Build|Registration)'`
  - evidence: `core/types/redaction.go`, `std/redact/redact.go`, `core/redactor_options.go` (`WithRedactor`, `WithRedactionPolicy`), `core/stores/redaction_maps_memory.go`; the map is read back by tenant and session on resume and never enters a checkpoint (`core/checkpoint_envelope.go`, `core/redaction_sinks_test.go`).
- [x] 1.5 Integrate residency through `RegionProvider.Region() string` at Build and freeze the map/region shapes consumed by `m1-subject-erasure-residency`; keep `EraseSubject` outside this package.
  - acceptance: every configured store and reachable profile is checked for residency-bound tenants; absent or empty store region fails closed; error identifies tenant, target, required region, and actual region.
  - verify: `go test -timeout 10m ./core -run 'Test.*Residency.*Build'`
  - evidence: `core/stores/stores.go` (`RegionProvider`), `core/build.go` (`checkResidency`), `core/residency_build_test.go` (`redaction.residency-enforced-at-build`). Scope note: `EraseSubject` was implemented in `core/erasure.go` before the dedicated erasure change started; it satisfies the store contract and keeps its own tests (`core/erasure_test.go`), so the later change records evidence rather than re-implementing.

## Implementation

### 2. Detector rules and stable redaction

- [x] 2.1 Implement checksummed `std/redact` rules, card Luhn validation, IBAN MOD-97 validation, and immutable defaults. Verify malformed checksum rejection and invalid candidates unchanged.
  - verify: `go test -timeout 2m ./std/redact -run 'Test(Rule|Card|IBAN)'`
  - evidence: `std/redact/redact.go` (`TestRulesAndValidation` in `std/redact/redact_test.go`).
- [x] 2.2 Implement tenant-keyed HMAC tokens, collision-safe `RedactionMap`, stable session pseudonyms, and the approved flow registration seam. Verify identical entities map identically within a session and differently across tenants or sessions.
  - verify: `go test -timeout 2m ./std/redact -run 'Test(Redact|Token|Key)'`
  - evidence: `std/redact/redact.go`, `core/redaction_scenarios_test.go` (`redaction.pseudonym-stable-within-session`).

### 3. Prompt and guard integration

- [x] 3.1 Implement `std/guard.RedactionGuard` and bind it before assembly in the resolved native turn path. Preserve the map across Send and Resume.
  - verify: `go test -timeout 10m ./core -run 'Test.*Redaction.*(Build|Send|Resume)'`
  - evidence: `std/guard/redaction.go`, `core/native_effects.go` (`nativeAssemble` redacts before the model request), `core/redaction_assembly_test.go`.
- [x] 3.2 Redact input, tool-result, and provider context blocks before the model request, including custom assembler flows. Verify a rewritten input is the only input visible to the model and history.
  - verify: `go test -timeout 10m ./core -run 'Test.*Redaction.*PublicPath'`
  - evidence: `core/redaction_scenarios_test.go` (`guards.redaction`).

### 4. Store write protection

- [x] 4.1 Protect SessionLog writes in `core/conversation.go` and lifecycle/steer append paths. Verify `Append` never receives the raw seeded entity.
  - verify: `go test -timeout 10m ./core -run 'Test.*Redaction.*SessionLog'`
  - evidence: `core/conversation.go`, `core/drive_lifecycle.go` (steer appends keep the steer identity and redact only when configured).
- [x] 4.2 Protect EventLog, checkpoints, OutputStore, and notes writes; persist maps separately with tenant/session binding and retention. Verify each configured sink using intercepting stores.
  - verify: `go test -timeout 10m ./core ./std/notes ./std/outputs -run 'Test.*Redaction.*(Event|Checkpoint|Output|Notes)'`
  - evidence: `core/conversation.go` (event payloads), `core/drive_lifecycle.go` (checkpoint state), `std/notes/notes.go`, `core/stores/stores.go` (`OutputStore`, `EventLog`, `Notes` ports), `core/redaction_sinks_test.go` (`redaction.stores-never-see-raw-entities`).
- [x] 4.3 Assert that no telemetry path receives content. Apply the redactor when a live content-capture seam lands.
  - verify: `go test -timeout 10m ./core -run 'Test.*Redaction.*Telemetry'`
  - evidence: `core/redaction_telemetry_test.go` (`redactedRunCarriesNoEntity`) captures span attributes and structured log records over a real redacted turn and asserts no raw entity reaches either; `core/conversation_telemetry_test.go` proves a fresh `Send` records the run span through the configured handler, since the lifecycle telemetry seam was only wired on the recovery path before.

### 5. Output rehydration and residency

- [x] 5.1 Rehydrate full tokens inside the existing `Windowed.GuardStream` release boundary, aligned with the effective output window. Drop unknown tokens and count `gohan.redact.unknown_token` without content attributes.
  - verify: `go test -timeout 10m ./std/guard ./core -run 'Test.*(StreamingRehydration|HallucinatedToken)'`
  - evidence: `std/guard/output.go` (windowed release rehydrates through the run map), `core/redaction_scenarios_test.go` (`redaction.streaming-rehydration`, `redaction.hallucinated-token-dropped`).
- [x] 5.2 Enforce model and store region compatibility in `Build`; reject missing region evidence for configured residency-bound stores and include tenant, target, required region, and actual region in the typed error.
  - verify: `go test -timeout 10m ./core -run 'Test.*Residency.*Build'`
  - evidence: `core/build.go` (`checkResidency`), `core/residency_build_test.go`.

### 6. Scenario and package gates

- [x] 6.1 Add public-path tests named exactly `redaction.pseudonym-stable-within-session`, `redaction.stores-never-see-raw-entities`, `redaction.streaming-rehydration`, `redaction.hallucinated-token-dropped`, `redaction.residency-enforced-at-build`, and `guards.redaction`. Cover deterministic boundaries plus Build, Send, and Resume; unit-only tests do not satisfy acceptance.
  - verify: `go test -timeout 10m ./... -run 'Test.*(Redaction|Residency)'`
  - evidence: `core/redaction_scenarios_test.go`, `core/redaction_sinks_test.go`, `core/residency_build_test.go`; `task spec:gate GATE=M1` reports `unregistered=0` and no missing redaction id.
- [x] 6.2 Run repository lint, test, spec, API, and example gates after implementation and resolve failures.
  - verify: `task lint && task test && task spec && task api:check && task examples:run`
  - evidence: `task lint` 0 issues; `task test` green; `task spec` `missing_in_scope=0`; `task api:check` reports pre-v1 incompatible changes without failing; `task examples:run` executes `invoice.send`.
- [x] 6.3 Validate the authored change after blockers resolve; do not require the full M1 gate before other owners land.
  - verify: `openspec validate m1-redaction-pipeline --strict --no-interactive`
  - evidence: `Change 'm1-redaction-pipeline' is valid`.

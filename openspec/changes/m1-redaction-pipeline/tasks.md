# Tasks

## Authoring

- [x] 1.1 Assign the six M1 scenario IDs and preserve separate subject-erasure ownership.
  - evidence: `openspec/changes/m1/design.md:115-128`; package delta lists only the five redaction IDs and `guards.redaction`.
- [x] 1.2 Map the current assembly, output, store, and Build seams. Record absent policy registration, map persistence, and store-region contracts as blockers.
  - evidence: `design.md` D2-D6; `core/native_options.go`, `core/native_resolve.go`, `core/stores/stores.go`, `std/guard/output.go`.
- [x] 1.3 Define deterministic public-path test seeding and existing-service-strict evidence requirements.
  - evidence: `design.md` D7; tasks 2-6 require store interception and public Build/Send/Resume tests.
- [ ] 1.4 Resolve the core-owned policy/map registration contract and any required normative amendment/ADR before implementation admission.
  - verify: `openspec validate m1-redaction-pipeline --strict --no-interactive`
- [ ] 1.5 Define redaction-map persistence/retention and region metadata for every configured store and event/output/notes port. Do not admit implementation while a named sink lacks a concrete port or region source.
  - verify: `openspec validate m1-redaction-pipeline --strict --no-interactive`

## Implementation

### 2. Detector rules and stable redaction

- [ ] 2.1 Implement checksummed `std/redact` rules, card Luhn validation, IBAN MOD-97 validation, and immutable defaults. Verify malformed checksum rejection and invalid candidates unchanged.
  - verify: `go test -timeout 2m ./std/redact -run 'Test(Rule|Card|IBAN)'`
- [ ] 2.2 Implement tenant-keyed HMAC tokens, collision-safe `RedactionMap`, stable session pseudonyms, and the approved flow registration seam. Verify identical entities map identically within a session and differently across tenants or sessions.
  - verify: `go test -timeout 2m ./std/redact -run 'Test(Redact|Token|Key)'`

### 3. Prompt and guard integration

- [ ] 3.1 Implement `std/guard.RedactionGuard` and bind it before assembly in the resolved native turn path. Preserve the map across Send and Resume.
  - verify: `go test -timeout 10m ./core -run 'Test.*Redaction.*(Build|Send|Resume)'`
- [ ] 3.2 Redact input, tool-result, and provider context blocks before the model request, including custom assembler flows. Verify a rewritten input is the only input visible to the model and history.
  - verify: `go test -timeout 10m ./core -run 'Test.*Redaction.*PublicPath'`

### 4. Store write protection

- [ ] 4.1 Protect SessionLog writes in `core/conversation.go` and lifecycle/steer append paths. Verify `Append` never receives the raw seeded entity.
  - verify: `go test -timeout 10m ./core -run 'Test.*Redaction.*SessionLog'`
- [ ] 4.2 Protect EventLog, checkpoints, OutputStore, and notes writes; persist maps separately with tenant/session binding and retention. Verify each configured sink using intercepting stores.
  - verify: `go test -timeout 10m ./core ./std/notes ./std/outputs -run 'Test.*Redaction.*(Event|Checkpoint|Output|Notes)'`
- [ ] 4.3 Add opt-in telemetry content protection before capture. Verify raw entities are absent from captured content and telemetry labels.
  - verify: `go test -timeout 2m ./core -run 'Test.*Redaction.*Telemetry'`

### 5. Output rehydration and residency

- [ ] 5.1 Rehydrate full tokens inside the existing `Windowed.GuardStream` release boundary, aligned with the effective output window. Drop unknown tokens and count `gohan.redact.unknown_token` without content attributes.
  - verify: `go test -timeout 10m ./std/guard ./core -run 'Test.*(StreamingRehydration|HallucinatedToken)'`
- [ ] 5.2 Enforce model and store region compatibility in `Build`; reject missing region evidence for configured residency-bound stores and include tenant, target, required region, and actual region in the typed error.
  - verify: `go test -timeout 10m ./core -run 'Test.*Residency.*Build'`

### 6. Scenario and package gates

- [ ] 6.1 Add public-path tests named exactly `redaction.pseudonym-stable-within-session`, `redaction.stores-never-see-raw-entities`, `redaction.streaming-rehydration`, `redaction.hallucinated-token-dropped`, `redaction.residency-enforced-at-build`, and `guards.redaction`. Cover deterministic boundaries plus Build, Send, and Resume; unit-only tests do not satisfy acceptance.
  - verify: `go test -timeout 10m ./... -run 'Test.*(Redaction|Residency)'`
- [ ] 6.2 Run repository lint, test, spec, API, and example gates after implementation and resolve failures.
  - verify: `task lint && task test && task spec && task api:check && task examples:run`
- [ ] 6.3 Validate the authored change after blockers resolve; do not require the full M1 gate before other owners land.
  - verify: `openspec validate m1-redaction-pipeline --strict --no-interactive`

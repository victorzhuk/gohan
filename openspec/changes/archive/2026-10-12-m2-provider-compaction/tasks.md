# Tasks

Commands use module boundaries: `go -C adapter/openai test ...` for an adapter module. Every verification command carries `timeout` and a test-time limit. A `-run` expression that matches no test is a failure, not a pass; each acceptance check names the subtest it must match.

## 1. Provider compaction in the adapters

- [x] 1.1 Add `adapter/anthropic/compact.go`: a `ProviderCompact` value implementing `stores.Compactor` over the adapter's existing `Config` (`adapter/anthropic/anthropic.go:29-47`), calling the `compact` primitive and returning `types.Compaction{Kind: types.CompactionText, CoversUpTo: h.Version, Summary, Model: p.Name}` whose summary blocks carry `Origin{Kind: OriginModel}`. It MUST refuse `CompactionNone` and any mode it does not serve with an error naming the profile and the mode, MUST fold only the covered history it is handed, and MUST expose its served-mode declaration. Verify: `timeout 2m go test -timeout 2m ./adapter/anthropic -run 'TestProviderCompact'`.
  - evidence: `adapter/anthropic/request.go`; `core/stores/projection.go:24-33`; `core/types/message.go:156-172`; `docs/design/evidence.md:16`; design D1, D2.
- [x] 1.2 Add `adapter/openai/compact.go`: a `ProviderCompact` over `/responses/compact` returning `types.Compaction{Kind: types.CompactionOpaque, CoversUpTo: h.Version, Opaque: Raw{Provider: <endpoint>, Value: <item>}, Model: p.Name}`. The returned item MUST pass through the request encoder byte-identically and MUST never be pruned or rewritten (`adapter/openai/request.go`). It MUST refuse `CompactionNone` and any mode it does not serve, naming the profile. Verify: `timeout 2m go test -timeout 2m ./adapter/openai -run 'TestProviderCompact'`.
  - evidence: `adapter/openai/openai.go:19-45`; `core/types/message.go:156-172`; `docs/design/evidence.md:16`; design D1, D2.
- [x] 1.3 Prove both compactors against their module's stub transport with fixture bodies: a text compaction, an opaque compaction, and the refusal for an unserved `Caps.Compaction`. Verify: `timeout 2m go test -timeout 2m ./adapter/anthropic ./adapter/openai -run 'TestProviderCompact'` executes each named subtest and reports none missing.
  - evidence: `adapter/openai/openai.go:29-32`; design D2, Risks.

## 2. Model-bound opaque view

- [x] 2.1 Add one shared predicate beside `keepBlock` (`core/fidelity_projection.go:75-81`) — an opaque compaction survives only when its `Model` equals the target — and use it from both `keepBlock` and the collapse. No behaviour change for `ProjectFidelity`. Verify: `timeout 2m go test -timeout 2m ./core -run 'TestProjectFidelity|TestProjectFidelityKeepsBoundOpaque'` executes.
  - evidence: `core/fidelity_projection.go:75-81`; design D3.
- [x] 2.2 Change `collapseCompactionHistory` to take the assembled endpoint identity and drop the selected block when it is `CompactionOpaque` and bound elsewhere, rendering no summary and leaving the covered messages visible; a `CompactionText` block keeps today's behaviour. Verify: extend `TestCollapseCompactionHistory` (`core/compaction_assembly_test.go:59`) with the opaque-drop and text-retain cases and run `timeout 2m go test -timeout 2m ./core -run 'TestCollapseCompactionHistory'`.
  - evidence: `core/native_effects.go:328-360`; `core/types/assembly.go:29-38`; design D3, D4.
- [x] 2.3 Confirm the target identity reaching the collapse is the endpoint that sends the request on the fallback path (`openspec/specs/model/spec.md:290`, `:450`), and thread that target at the re-assembly point if the profile is stale there. Verify: a test asserts the profile the collapse received equals the endpoint that assembles the request; it fails while the primary assembly is reused and passes once the target is threaded.
  - evidence: `core/native_effects.go:328-335`; `openspec/specs/model/spec.md:290,450`; design D4.

## 3. Public-path scenario binding

- [x] 3.1 Register `context.opaque-bound-to-model` in `TestCompactionPublicPath` (`core/compaction_public_path_test.go:179`) with a native-flow variant and a foreign-flow variant: a flow whose policy carries an opaque-producing compactor persists a `CompactionOpaque` block bound to its endpoint, and when the other endpoint assembles, assembly drops the block and the request is re-fitted through the declared projections and `Summarize`. Verify: `timeout 10m go test -timeout 10m ./core -run 'TestCompactionPublicPath/context\.opaque-bound-to-model'` reports ok with both variants executed.
  - evidence: `core/compaction_public_path_test.go:179`; design D3, D6.
- [x] 3.2 Register `context.opaque-fallback-rejected` through `Build`: a flow requesting `ProviderCompact` on a profile with `Caps.Compaction == CompactionOpaqueCap` and a `Fallback` naming another provider MUST fail `Build` with an error naming the flow, the profile and the strategy; the same flow on a `CompactionTextCap` profile MUST build. Verify: `timeout 10m go test -timeout 10m ./core -run 'TestCompactionPublicPath/context\.opaque-fallback-rejected'`.
  - evidence: `core/build_fidelity.go:35-58`; `core/native_resolve.go:154`; `core/build_fidelity_test.go:90`; design D5.
- [x] 3.3 Confirm the `build.opaque-compaction-fallback` subtest (`core/build_fidelity_test.go:90`) and every `m2-context-persisted-compaction` subtest still pass unchanged, and that no scenario id outside the two owned ones is claimed or renamed. Verify: `timeout 10m go test -timeout 10m ./core -run 'TestCheckFidelity|TestCompactionPublicPath|TestCollapseCompactionHistory'` executes and reports no subtest missing.
  - evidence: `openspec/changes/m2/design.md:66-67`; design D6.

## 4. Artifact and repository gates

- [x] 4.1 Validate the change artifact: `timeout 60s openspec validate m2-provider-compaction --strict --no-interactive` exits 0, and the `context` delta's MODIFIED block reproduces the main requirement and all seven scenarios verbatim (a diff of the extracted blocks is empty) while the delta adds no scenario id.
  - evidence: `openspec/specs/context/spec.md:131-166`; design Non-Goals.
- [x] 4.2 Run the repository gates after implementation: `timeout 10m task lint && timeout 30m task test` pass, and `timeout 5m task spec:gate GATE=M2` reports no new `missing_in_scope` for `context.opaque-bound-to-model` and `context.opaque-fallback-rejected`. Record the observed output; a gate that has not been run is not evidence.
  - evidence: `Taskfile.yml:28-36`, `:69-84`; `openspec/changes/m2/tasks.md` (M2 is a milestone gate).
- [x] 4.3 Record the new exported adapter symbols before the v1 freeze: `timeout 5m task api:check` lists the two `ProviderCompact` types, their constructors and their served-mode declarations, and reports no changed frozen declaration.
  - evidence: `timeout 5m task api:check` exit 0. Only tags `v0.1.0` and `adapter/otel/v0.1.0` exist (HEAD `e49c61a`). `adapter/anthropic` and `adapter/openai` carry the new `ProviderCompact` symbols but print `no tag yet, skipped (no baseline to diff against)` — the expected weak signal for additive exports. `adapter/otel` reports `exported API compatible with adapter/otel/v0.1.0`. Root module diffs HEAD against `v0.1.0` and reports pre-v1 incompatible changes without failing: `module is pre-v1, reporting incompatible change(s) without failing`. No frozen declaration changed.
  - evidence: `Taskfile.yml:51-67`; design Migration Plan.
- [x] 4.4 Run `timeout 2m task spec:types` only if a Contract code block changed. This change adds no normative declaration and edits no main spec, so the expected result is no diff; if a Contract block did change, regenerate `docs/design/types.md` so `git diff --exit-code docs/design/types.md` is clean after regeneration.
  - evidence: `Taskfile.yml:9-13`; design Non-Goals.

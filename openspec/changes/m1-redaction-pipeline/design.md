# Design: m1-redaction-pipeline

## Context

The M1 matrix assigns `redaction.pseudonym-stable-within-session`, `redaction.stores-never-see-raw-entities`, `redaction.streaming-rehydration`, `redaction.hallucinated-token-dropped`, `redaction.residency-enforced-at-build`, and `guards.redaction` here. All are recorded missing because no `std/redact` exists (`openspec/changes/m1/design.md:115-124`). `Tenant.Residency` is in `core/types/identity.go:73`; `ModelProfile.Region` is in `core/types/model_profile.go:15`. The `core/stores.Stores` ports are `SessionLog`, `Runs`, `Checkpoints`, and `Journal` (`core/stores/stores.go:6-11`). No inspected store implementation or port exposes `Region`; Build cannot currently prove store residency.

The normative contract is `openspec/specs/redaction/spec.md:12-91` and `openspec/specs/guards/spec.md:65-73,100-103`. `std/guard/output.go` provides `OutputGuard`, `Windowed`, `NewWindowed`, `GuardStream`, and `release`; `Windowed.Window` defaults to 64 tokens. `core/drive_turn.go:260` invokes the resolved assembler, while `std/assembly.go:123` (`StablePrefix.Assemble`) builds messages from input, history, and providers. These are real seams, not evidence that all custom assemblers or store writes are already covered.

## Goals and non-goals

**Goals:** implement the six assigned public-path behaviors, honor fixed placement, and provide one reusable redaction map and key contract for the erasure owner.

**Non-goals:** `EraseSubject`, erasure records or deletion, assigning the four erasure scenario IDs, changing other M1 owners' scenario assignments, and claiming coverage through synthetic child results.

## Decisions

### D1 — `std/redact` API and deterministic tokens

Create `std/redact` with these exported types and functions:

```go
type Rule struct {
    Name string
    Kind types.EntityKind
    Pattern string
    Checksum ChecksumKind
}
type ChecksumKind int
const (NoChecksum ChecksumKind = iota; CardChecksum; IBANChecksum)
func DefaultRules() []Rule
func ValidateRule(rule Rule) error

type RedactionPolicy struct {
    Detectors []Detector
    Mode TokenMode
    Preserve []types.EntityKind
}
type TokenMode int
const (HMACToken TokenMode = iota; FormatPreserving; Vaulted)
type Detector interface {
    Detect(context.Context, []types.Block) ([]Span, error)
}
type Span struct { Block, Start, End int; Kind types.EntityKind }
type Entity struct { Kind types.EntityKind; Value string }
type RedactionMap struct { Tenant, Session string; Entries map[string]Entity }
type Redactor interface {
    Redact(context.Context, []types.Block, RedactionPolicy) ([]types.Block, RedactionMap, error)
    Rehydrate(context.Context, []types.Block, RedactionMap) ([]types.Block, error)
}
type KeySource interface { Key(context.Context, string) ([]byte, error) }
func New(keySource KeySource) *DefaultRedactor
```

Use `core/types.EntityKind` and corresponding `EntityPerson`, `EntityEmail`, `EntityPhone`, `EntityCard`, `EntityIBAN`, and `EntityCustom` constants; do not introduce a second kind vocabulary. `DefaultRules` returns immutable compiled rule definitions whose SHA-256 checksum is fixed in package data. `ValidateRule` rejects a checksum mismatch before Build or first use. Card candidates pass Luhn validation; IBAN candidates pass MOD-97 validation; invalid candidates remain unchanged. Rule checksums validate the shipped rule data, not input entities.

HMAC mode calls `KeySource.Key(ctx, tenant)` for a tenant key. Derive token bytes as HMAC-SHA-256 over `session || NUL || kind || NUL || canonical-value`; encode a fixed prefix and unpadded base32 digest, e.g. `[PERSON_<digest>]`. The session component ensures same-entity stability within a session and separation across sessions; tenant-keyed keys prevent cross-tenant linkage. A token collision with a different entity is an error, never an overwrite. The `RedactionMap` entry maps complete token to `{Kind, Value}` and records exact tenant and session. `FormatPreserving` and `Vaulted` must not claim support until complete implementations meet the same privacy and rehydration contracts; Build rejects unsupported mode with a typed `ErrModeUnsupported`.

### D2 — Registration and policy handoff are blocked on a shared type seam

The live `core.NativeSpec` in `core/native_options.go` is in package `gohan`; it cannot import `std/redact` because `std` imports `core`. The normative policy and detector types belong in `std/redact`, but there is no live core-compatible `NativeSpec` field or registration option. Do not add an `any` field or a second builder. The implementation admission is blocked until the driver defines a core-owned callback/opaque contract and the required normative amendment/ADR. Once approved, the design must name the exact field and option and prove policy reaches `nativeAssemble` on Build, Send, and Resume. Until then, proposal-level scope remains intact, but no coder may choose an identifier or bypass the flow policy.

### D3 — Guard stage integration

The existing `guards.Guard` is `types.Decider[guards.GuardInput, guards.GuardVerdict]` (`core/guards/guard.go:43`); verdict `Rewrite` already carries replacement blocks. Add `std/guard.RedactionGuard` implementing `Decide(context.Context, guards.GuardInput) (types.Decision[guards.GuardVerdict], error)`. It invokes the flow-bound `std/redact.Redactor.Redact` only for `types.StageInput`, returns `Rewrite` and redacted blocks when changed, and returns `Pass` otherwise. It must never run for unrelated stages. It must retain the resulting map in flow/run state through a core-owned approved seam; absent that seam, input rewrite alone cannot meet later storage or output requirements.

`core/drive_turn.go` function `turnConfig` (the existing assembled turn path) invokes `c.assemble` at line 260; add the `StageInput` guard before assembly there, so `Rewrite` replaces the input supplied to the model and the history append path. `core/native_effects.go:nativeAssemble` currently composes the assembler and prompt decoration, not a guard chain. `core/drive_lifecycle.go` and `core/resume.go` must restore the same flow policy and map for resumed calls. The exact configuration and map-carrying APIs remain blocked with D2; do not claim `guards.redaction` passes until public `Send` and `Resume` demonstrate it.

### D4 — Required placement and call-site inventory

Placement is fixed by the normative capability contract:

1. **Prompt input and context:** `std/assembly.go:StablePrefix.Assemble` / `build` at lines 123-182 assembles history, input, and `ContextProvider` output. Redact user input, tool results, and provider blocks before constructing the request. Core invokes this resolved assembler from `core/drive_turn.go:turnConfig` at line 260. Custom `NativeSpec.Assemble` functions are a bypass unless the approved core wrapper enforces the policy outside the callback.
2. **Session history writes:** `core/conversation.go:conversation.appendInput` (line 274) and the initial append in `Conversation.Send` (line 205); runtime history writes also pass through `core/drive_lifecycle.go`'s `HistoryAppender` and `core/conversation_steer.go`'s steer append path. Redact each message before `SessionLog.Append`, retaining the separate map under tenant/session retention.
3. **Event writes:** `core/conversation.go:conversation.relay` (line 363) writes `stores.EventLog`; redact event payload blocks before append. `core/stores.Stores` has no `EventLog` member, so `WithConversationEventLog` (`core/conversation.go:117`) is a separate configured port and must receive the same policy.
4. **Output and checkpoints:** `core/drive_lifecycle.go` owns checkpoint creation through its lifecycle appender/checkpoint ports; `core/checkpoint_envelope.go:encodeCheckpoint` serializes runtime state. Redact checkpoint content before `Checkpoints.Put` without corrupting opaque runtime state. Current checkpoint shape has no redaction-map reference; approved envelope integration is required.
5. **Notes:** `std/notes/notes.go` is the notes tool and its store writes. Redact note content before its store call, after the `StageContext` guard. The notes store is outside `core/stores.Stores`.
6. **Telemetry content capture:** no current content-capture option was found in `core/telemetry.go`; when enabled later, redact before capture. This does not authorize raw content capture today.

This inventory exposes a blocking ownership/interface gap: EventLog, OutputStore, notes, and redaction-map persistence are not all carried by `core/stores.Stores`, and no map store port exists. Implementation admission requires a shared contract specifying these ports and map retention. Do not claim the stores-never-see-raw scenario until every named sink has a test interceptor proving its received values are redacted.

### D5 — Streaming output and unknown tokens

Join rehydration inside `std/guard/output.go:Windowed.GuardStream` at the existing `flush` closure, before `release` and before any call to `emit`. Do not add a second independent windower. Set the output guard's rehydration window equal to the effective `Windowed.Window`; if the existing constructor receives `window <= 0`, use `DefaultWindow` (64). Each release window must include token-safe boundaries: buffering starts before a token's first byte and flushes only after its closing delimiter, even if this requires retaining the current incomplete token in addition to the word-count window. A pseudonym split across provider chunks is reassembled before token lookup.

`Rehydrate` replaces only full tokens present in the run's map. A token-shaped unknown value is deleted, never emitted. Count `gohan.redact.unknown_token` with no entity value, token text, session, or tenant attributes; use only a bounded `reason=unknown_token` attribute if telemetry requires a label. The map must be available before stream release; missing map or key fails closed and releases no pseudonym text. The output stream uses existing `GuardStream` emission in `core/model_stream.go` and its consumer; verify the public `Conversation.Send` path, not only `Windowed` unit tests.

### D6 — Residency and actual store coverage

At Build, validate each tenant's `RunInfo.Residency` against every reachable `ModelProfile.Region` and each configured store's declared region. Include tenant, profile/store name, required region, and actual region in a typed `ErrResidencyMismatch`; empty region is not evidence of compliance. Router candidates and fallbacks count as reachable, not only the primary model. Runtime routing cannot override the result.

`ModelProfile.Region` and `RunInfo.Residency` exist. The inspected `core/stores.Stores` and repository store implementations expose no `Region`, and there is no common configured store-region metadata. Therefore the current Build path cannot implement this requirement honestly. Do not silently assign a default region or treat absent stores as compliant. Admission remains blocked until the driver specifies the shared region interface/configuration and decides how empty required regions fail. This package must not edit `openspec/specs/**`; if satisfying the normative per-store Region requirement needs a spec change, the driver owns that amendment and its ADR.

### D7 — Deterministic public-path tests

Seed tests with the repository's public mechanisms: create a memory `SessionLog`, `EventLog`, checkpoints, a deterministic `KeySource`, and an instrumented model that captures each `ModelRequest`; register a native flow via `WithNativeAgent`, call `Build`, then exercise `Conversation.Send` and checkpoint `Resume`. Intercept store `Append`, event `Append`, checkpoint `Put`, and notes writes to assert raw entities never arrive. Use fixed detector rules and keys; never call external NER/LLM services. Send streamed chunks that split a valid token across the `Windowed` boundary and a model-invented token; assert only the real entity is emitted and unknown-token telemetry increments exactly once. Run the same proofs after resume. Build residency cases with two registered models whose `ModelProfile.Region` differs and router/fallback reachability; store-region cases remain blocked until the missing region contract is approved.

## Risks and admission blockers

This is not implementation-ready for admission. The required flow policy/map registration contract, redaction-map store and retention port, complete configured-store inventory, and region metadata mechanism are absent from the live API. These are contract decisions, not coder choices. No normative spec or ADR changes are made in this package. The six scenario IDs remain assigned here, but none is claimed covered until the public-path evidence exists.
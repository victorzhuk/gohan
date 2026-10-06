# 0157. Capability ports live in core, stored records carry their version in an envelope, and the journal keeps a call's arguments

Status: accepted

## Context

Four M1 change packages were authored against the shipped code and every one of them
stopped at the same wall: a capability that lives in `std` or in an adapter reaches the
driver only through a port declared in `core`, and the ports these four need do not exist
or do not carry the data the requirement is written against.

- **Redaction** has no port at all: `openspec/specs/redaction/spec.md:12-95` declares
  `Redactor`, `RedactionMap`, `KeySource` and the placements, `openspec/specs/build/spec.md`
  already lists `WithRedactor(r Redactor) Option`, and neither `core` nor `std` has the
  type. The placement rules also name `SessionLog`, `EventLog`, `OutputStore`,
  `Checkpoints` and `notes` as sinks, and the redaction map is stored "separately,
  tenant-keyed, with its own retention" — and `stores.Stores` has no such store.
- **Residency** is enforced at `Build` "when any reachable router target or configured
  store for a residency-bound tenant is outside the region"
  (`openspec/specs/redaction/spec.md:88-92`). `Tenant.Residency`
  (`core/types/identity.go:73`) and `ModelProfile.Region`
  (`core/types/model_profile.go:15`) exist; no store port or implementation exposes a
  region, so the rule cannot be evaluated.
- **Verification** (`openspec/specs/tools/spec.md:137`) runs `Verify` "on `Resume`/`Recover`
  for every journal entry still `Unknown`". A journal entry records a fingerprint, not the
  arguments, and `Journal` cannot enumerate the session's `Unknown` entries, so the
  trigger cannot be reached and `Verify(ctx, args, result)` has no `args`.
- **Schema evolution** (`openspec/specs/stores/spec.md:334`) states that all eight stored
  shapes carry `SchemaVersion` and that upcasters run on read. Only
  `stores.Checkpoint.SchemaVersion` exists (`core/stores/checkpoint.go:39-52`); the other
  seven shapes have no version, no envelope and no reader that consults
  `core/stores/schema.go`'s registry.
- **Provider adapters** must refuse a static `Extra` key that collides with a governed
  field at `Build` and a dynamic one at call time
  (`openspec/specs/model/spec.md:325-329`). A native flow has no declared
  `types.ModelOptions`, so there is no static value to validate.
- A seventh fact sits under the first six: `std.UncertainOutcomeError`
  (`std/uncertainty.go:15`) is a second type with the same name as
  `types.UncertainOutcomeError` (`core/types/errors.go:133`), and `types.ProblemOf`
  matches only the core one (`core/types/problem.go:307`) — a run that ends with the
  `std` value is reported as an internal error instead of `catalog[35]`.

## Decision

1. **Every capability port is declared in `core` and implemented outside it.**
   `core/types/redaction.go` gains `Redactor`, `RedactionPolicy`, `TokenMode`,
   `EntityKind`, `Entity`, `Span`, `Detector` and `RedactionMap`; `std/redact` implements
   `Redactor`. Registration uses the option the `build` contract already declares,
   `WithRedactor(types.Redactor)`, resolved into the native configuration the same way
   `NativeSpec.Decider` and `NativeSpec.TaintPolicy` are. No second builder and no
   `any`-typed field: a flow that wants redaction configures a `Redactor`.
2. **The redaction map is a store of its own.** `stores.Stores` gains `RedactionMaps`
   with `Put(ctx, tenant, session, map)` and `Get(ctx, tenant, session)`, tenant-keyed
   and retained independently of run state, so a map outlives the checkpoint it was
   written beside and is erased by its own retention rule. Redaction is applied by the
   driver before a write, never delegated to a store.
3. **Residency is checked through an optional interface at `Build`.** `RegionProvider`
   with `Region() string` lives beside the store ports; when the tenant's `Residency` is
   set, every configured store must implement it and return a non-empty region, and every
   reachable profile's `Region` must match. A missing interface or an empty region fails
   `Build` naming the tenant, the store or profile, and the regions. Fail-closed: an
   implementation that cannot state its region cannot serve a residency-bound tenant.
4. **The journal keeps a call's canonical arguments and can list unknown entries.**
   `Journal.Reserve` takes the canonical argument bytes alongside the fingerprint, and
   the journal exposes the session's `Unknown` entries with them. A `Verify` run at
   `Send`, `Resume` or `Recover` therefore has the arguments the effect was attempted
   with, and arguments are never inferred from projected history. This is a port change,
   permitted while the root module stays pre-v1 (`docs/design/compatibility.md:33-46`);
   after the v1 freeze the same growth happens through an optional interface.
5. **A `Failed` reconnaissance preserves the recorded `ToolError`.** `Verify` returns
   `types.Outcome` only, so reconciliation changes the outcome and never invents detail:
   the entry keeps the error the effect recorded, and `*UncertainOutcomeError` is
   returned only for entries with no `Verify` or whose `Verify` errored. The audit record
   for a reconciliation pins `ResultSHA` and `ResultBytes` to the canonical encoding of
   `types.Message{Blocks: result.Content}`, the same recipe `OutputStore` uses, so a
   checksum in the audit trail can be recomputed from the stored output.
6. **One `UncertainOutcomeError`.** `std.UncertainOutcomeError` becomes a type alias of
   `types.UncertainOutcomeError` and `std.NewUncertainOutcomeError` returns that type, so
   `types.ProblemOf` classifies a run that ends uncertain as uncertain rather than as an
   internal error. No caller changes: the alias keeps the `std` name and constructor.
7. **A stored record carries its version in an envelope.** `stores.RecordEnvelope` holds
   the shape name, the `SchemaVersion` the record was written at and the raw payload;
   one generic `DecodeRecord[T]` consults the registry, applies the pure N→N+1 chain to
   the envelope's payload, then decodes, and emits `gohan.schema.upcast{from,to}` once
   per successful chain through the `types.Telemetry` already in the context. Domain
   types stay free of schema metadata; the stored record is never rewritten. The eight
   shapes in the `stores` contract are versioned by their envelope, and the readers that
   decode a persisted record are the ones that consult the registry.
8. **A static `Extra` collision is refused at `Build`.** `NativeSpec` gains
   `ModelOptions types.ModelOptions`; `Build` refuses an `Extra` key that collides with a
   governed field. A dynamic entry is supplied per request and stays the adapter's
   call-time duty (`ClassPermanent`), which the provider adapter contract already states.
9. **The conformance suite proves the contract; an adapter earns the coverage.**
   `testkit/conformance` keeps the reference double and the fixtures and can show the
   suite fails a non-conforming implementation; the scenario ids whose actor is an
   adapter stay unearned until `adapter/openai` and `adapter/anthropic` run the suite.
   `model.undeclared-fidelity` is earned through the existing `CheckFidelity` path at
   `Build`, and `model.extra-collision` through decision 8.

## Consequences

- `openspec/specs/{tools,stores,redaction,build}/spec.md` are edited with this ADR:
  the `tools` Verification requirement states the argument source and keeps the recorded
  error; the `stores` contract states the record envelope and the decode boundary, the
  journal's argument retention, `RedactionMaps` and `RegionProvider`; the `redaction`
  contract states the core-owned port and the sink inventory; the `build` contract states
  the `Stores` field, the `NativeSpec` model options and the new refusals. The `model`
  contract needs no edit: `model.extra-collision` already states both cases, and the
  static source is a `build` fact.
  `docs/design/types.md` is regenerated in the same commit.
- `Journal.Reserve`, `stores.Stores` and `NativeSpec` change shape. The root module is
  pre-v1 through M2, so no compatibility shim is added and the change is recorded in the
  changelog; `task api:check` reports it against the last tag, which is expected.
- `std.UncertainOutcomeError` keeps its name and constructor as an alias, so flow code
  that returns it compiles unchanged and its runs are now classified as uncertain.
- Four seams, one shape: a capability is declared in `core`, implemented in `std` or an
  adapter, and injected at `Build`. The next capability to arrive follows the same three
  steps instead of inventing a fourth.
- Rejected: importing `std/redact` from `core` (the core budget rule,
  `docs/design/architecture.md` §4.2a); an `any`-typed policy field on `NativeSpec`
  (it moves the type error from `Build` to the first call); storing redaction maps in the
  checkpoint (checkpoints expire with the run and are consumed once, while a map must
  outlive both); inferring a call's arguments from history for `Verify` (projection and
  compaction are allowed to change history, and `tools.verify-reconciles-unknown` has to
  hold after a resumed run).

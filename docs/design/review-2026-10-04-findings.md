# Review findings, 2026-10-04

This file records the cross-spec findings of the 2026-10-04 review exactly as the lenses returned them: severity, the two contradicting statements and the file:line evidence for each. The 27 items reconciled in that review are in ADR-0138; the rest are unreconciled and their reconciliation is the first task of the follow-up pass. The lenses are read-only and cite the corpus as it stood before the pass, so a line number may have moved.


## SpecConflictLens

Slice: all 35 capability specs. the 27 reconciled findings (ADR-0138) plus the clean-slice list.

```json
{
  "findings": "## Identifier and behaviour conflicts across the capability specs\n\n- BLOCKER | `ContextProvider` is declared twice in one spec with two different method sets: once with `Slot() ContextSlot` + `Provide(...)`, once with `Provide(...)` only. | `openspec/specs/assembly/spec.md:25` vs `openspec/specs/assembly/spec.md:48`\n- BLOCKER | `ContextSlot` and `Slot` are two distinct int types carrying the same member names `SlotStatic/SlotSession/SlotTurn`; `AssembleInput.Providers` is keyed by the second (`map[Slot][]ContextProvider`) while every provider must implement the first via `Slot() ContextSlot` — the key type and the provider-returned type cannot be the same. | `openspec/specs/assembly/spec.md:17-23`, `openspec/specs/assembly/spec.md:40-46`, `openspec/specs/assembly/spec.md:37`\n- BLOCKER | `Done` is declared without a `Seq` field, but a normative scenario asserts `Done.Seq == N` and a rule states the last delivered `Seq` is reported in `Done`; the `Run` struct it is also reported in has no `Seq` field either. | `openspec/specs/streams/spec.md:69` vs `openspec/specs/streams/spec.md:196` and `openspec/specs/streams/spec.md:94`, `openspec/specs/stores/spec.md:126-138`\n- BLOCKER | `Inspect` is specified to answer \"current turn, last `Seq`, pending tool calls, cost\" \"from stores only\", but the stored `Run` record exposes none of `Seq`, turn or pending calls — the claim contradicts the stored shape it must read. | `openspec/specs/recovery/spec.md:24` and `openspec/specs/tools/spec.md:303` vs `openspec/specs/stores/spec.md:126-138`\n- MAJOR | `MaxCost` is both \"required when `Pricing` is configured\" and \"defaults to 0 = unlimited\" with only a `Build` warning when unset outside `Batch` — a flow that must declare it versus one that may omit it. | `openspec/specs/limits/spec.md:48` vs `openspec/specs/limits/spec.md:50`\n- MAJOR | The same limit is reported under two `Limit` strings: `*LimitExceededError{Limit: \"MaxCost\"}` and `*LimitExceededError{Limit: \"cost\"}` for the identical rule. | `openspec/specs/limits/spec.md:67` vs `openspec/specs/limits/spec.md:74`\n- MAJOR | `RunLimits.MaxPendingApprovals` is written as a `RunLimits` field, but the normative `RunLimits` struct declares no such field. | `openspec/specs/permission/spec.md:98` and `openspec/specs/permission/spec.md:178` vs `openspec/specs/limits/spec.md:17-29`\n- MAJOR | The error catalog is declared \"closed\" with a row required for every sentinel a spec defines, yet `ErrToolDescription`, `ErrNotSuspendable`, `ErrVersionConflict` and `ErrStructuredOutput` are defined or required in specs with no catalog row. | `openspec/specs/messages/spec.md:206-229` vs `openspec/specs/tools/spec.md:281`, `openspec/specs/flow/spec.md:81`, `openspec/specs/stores/spec.md:297`, `openspec/specs/structured-output/spec.md:39`\n- MAJOR | Adapters are told to convert a provider call block to `ToolUse{Executor: ByProvider}`, but `ToolUse` has no `Executor` field; `Executor` is a `ToolSpec` field. | `openspec/specs/tools/spec.md:332` vs `openspec/specs/messages/spec.md:90-94` and `openspec/specs/tools/spec.md:45`\n- MAJOR | Mid-stream failure requires appending \"a trailing `Error` block\" to the persisted assistant message, but the closed `BlockKind` enum has no error kind and `ToolError` is not a `Block`. | `openspec/specs/model/spec.md:282` vs `openspec/specs/messages/spec.md:155-167` and `openspec/specs/messages/spec.md:133-136`\n- MAJOR | `ApproveScope` is declared `func ApproveScope(ttl time.Duration) ResumeInput` but invoked as `ApproveScope(op, 2h)` with a principal, while `identity` states `ApproveScope()` takes no principal — arity and signature both conflict. | `openspec/specs/permission/spec.md:91` and `openspec/specs/permission/spec.md:94` vs `openspec/specs/identity/spec.md:86`\n- MAJOR | `Checkpoint.Originator` is required to have an empty `Token` in storage, but it is a `Principal`, which has no `Token` field, and `identity` states the type \"cannot carry a token by type\" — the store rule asserts on a field the type forbids. | `openspec/specs/stores/spec.md:311` and `openspec/specs/stores/spec.md:341` vs `openspec/specs/stores/spec.md:62` and `openspec/specs/identity/spec.md:44-48,83`\n- MAJOR | `ClassContentPolicy` has two client-facing error shapes: surfaced \"always as `*GuardBlockedError{Stage: StageProvider}`\" versus the catalog row `gohan.content_policy` (Permanent, 422) sourced from `ModelError{ClassContentPolicy}`; `guards` never lists `StageProvider` as an output of a model refusal. | `openspec/specs/chains/spec.md:102` vs `openspec/specs/messages/spec.md:226`\n- MAJOR | Chain ordering constraints are declared over step names `Router` and `Hooks`, neither of which is a `StepKind` or appears in either prescribed chain order; `KindUser` is declared as a `StepKind` but appears in no chain listing. | `openspec/specs/chains/spec.md:55` vs `openspec/specs/chains/spec.md:26-37` and `openspec/specs/chains/spec.md:60-93`\n- MAJOR | `RunNotice.Reason` is specified as the `SuspendReason` name (`\"human_approval\"`) while a scenario pins it to `\"HumanApproval\"`, the Go constant identifier; a serialized notice cannot satisfy both. | `openspec/specs/streams/spec.md:125` vs `openspec/specs/suspension/spec.md:117` and `openspec/specs/suspension/spec.md:22`\n- MAJOR | `NoticeKind` is an `int` enum, but the notice contract scenario asserts a delivered body field `kind: \"finished\"`, a string name with no rendering rule. | `openspec/specs/streams/spec.md:47-54` vs `openspec/specs/streams/spec.md:130`\n- MAJOR | `messages` asserts the error catalog source for a denied tool is gate `Deny`, but the gate's deny verdict is `DenyVerdict` and `Deny` is a distinct `TaintAction` member, so the catalog cites an identifier that is neither. | `openspec/specs/messages/spec.md:219` vs `openspec/specs/permission/spec.md:21` and `openspec/specs/taint/spec.md:25`\n- MAJOR | One exported name carries incompatible kinds across specs, so a single `gohan` package cannot hold both: `Block` is a port interface in `messages` and an enum member of `GuardAction` in `guards`. | `openspec/specs/messages/spec.md:48` vs `openspec/specs/guards/spec.md:32`\n- MAJOR | Same class of collision: `Collect` is a generic drain function and an `OnError` enum member; `Continue` is a `Status` member and a `ResumeInput` constructor; `Suspended` is a `RunState` member and an event struct; `Failed` is an `Outcome` member and a `RunState` member; `Allow`/`Ask` are `Verdict` and `TaintAction` members; `Retryable` is an `ErrorKind` member and a wrapping function. | `openspec/specs/streams/spec.md:77` vs `openspec/specs/subflows/spec.md:33`; `openspec/specs/runtime/spec.md:29` vs `openspec/specs/suspension/spec.md:53`; `openspec/specs/stores/spec.md:120` vs `openspec/specs/streams/spec.md:56`; `openspec/specs/messages/spec.md:121` vs `openspec/specs/stores/spec.md:123`; `openspec/specs/permission/spec.md:20` vs `openspec/specs/taint/spec.md:24`; `openspec/specs/messages/spec.md:129` vs `openspec/specs/tools/spec.md:132`\n- MINOR | Blob spill-to-store is triggered by an undeclared `InlineBlobBytes` (default 64 KiB) while the same spec makes `Caps.Blobs.MaxBytes` the per-block cap — two thresholds decide one behaviour. | `openspec/specs/messages/spec.md:185` vs `openspec/specs/messages/spec.md:187`\n- MINOR | `RunLimits` defaults are stated as `MaxTurns 20`, `MaxToolCalls 50`, yet the named presets install `6/20` (Interactive), `50/200` (Agentic) and `20/100` (Batch); the zero-value default equals none of the three. | `openspec/specs/limits/spec.md:48` vs `openspec/specs/limits/spec.md:30-34`\n- MINOR | `MaxBlobBytes` and `MaxSandboxSeconds` appear in no preset literal, so both are 0 under `std.Interactive/Agentic/Batch` while other specs assign them non-zero defaults (256 MiB; per-tree seconds). | `openspec/specs/limits/spec.md:30-34` vs `openspec/specs/messages/spec.md:187` and `openspec/specs/sandbox/spec.md:74`\n- MINOR | The per-tree limit enumeration names only `MaxCost` as per-tree, but `MaxSandboxSeconds` is separately specified to bound cumulative seconds \"per tree\". | `openspec/specs/subflows/spec.md:50` vs `openspec/specs/sandbox/spec.md:74`\n- MINOR | `interop` states `SuspendReason` \"gains `AwaitingInput`\", but the owning capability already declares it as an existing member — the same identifier is claimed as new by one spec and established by another. | `openspec/specs/interop/spec.md:33` vs `openspec/specs/suspension/spec.md:26`\n- MINOR | `ShadowSuppressed` is used as a `ToolError` payload but is declared nowhere and has no catalog row, unlike the sibling `EgressDenied` which is a declared type. | `openspec/specs/release/spec.md:28` vs `openspec/specs/tools/spec.md:64-67`\n- MINOR | The `context` rule list is emitted out of order — 1–6, then 9, 10, then 8, then 7 — so the normative rule numbering does not match the prose sequence. | `openspec/specs/context/spec.md:50-58`\n- MINOR | The `identity` rule list is likewise out of order (1–5, 7, 6, 8) with no rules 9 or 10, so rule references inside other specs (\"identity rule 8\") resolve positionally rather than by number. | `openspec/specs/identity/spec.md:83-89` and `openspec/specs/tools/spec.md:103`\n\n## Slices with no conflict found\n\n- `Origin` / `OriginKind` / `Seq`: single declaration each (`messages/spec.md:32-46`, `streams/spec.md:72`); all cross-spec uses agree.\n- `Trust`: one declaration (`tools/spec.md:110-116`); `skills/spec.md:19,41` consumes it consistently.\n- `Caps` / `ModelRequest` / `ModelChunk` / `DeltaKind` / `Usage` / `Pricing`: declared only in `model`; every other spec (assembly, chains, runtime, messages, streams, structured-output) references them without restating a conflicting shape.\n- `Fingerprint`: defined once (`stores/spec.md:100`); `permission/spec.md:29` and `chains` agree on `hash(tool name, canonical JSON of args)`.\n- `Checkpoints` / `ResumeToken` / `ResumeInput` / `SuspendError` / `Waker`: single declaration in `stores`/`suspension`; `subflows`, `tools`, `identity`, `permission` consume without contradicting shape.\n- `Effect`: one enum (`tools/spec.md:17-23`); `runtime:115`, `sandbox:69`, `subflows:48` all derive from the same three members and agree on the ordering rule.\n- `RunState` / `Run` / `LeaseTTL` / `HeartbeatEvery`: single declaration in `stores`; `runtime:119` and `streams:97` cite the same constants.\n- `RunLimits` shape: the struct itself is declared once; the conflicts above are about individual field values and defaults, not about duplicate declarations.\n- `performance` / `lifecycle` / `telemetry`: prose-only or single-declaration specs; no cross-spec claim contradicts another.\n- `stores` retention/`Maintain` vs `working-state` notes retention: `working-state.notes-follow-working-retention` is consistent with the store-clock and purge rules; no conflict.\n\n## Unknowns\n\n- `docs/design/types.md` reports \"Duplicate definitions: none\" and \"Undefined identifiers: none\", yet the corpus contains the duplicates and undefined names listed above (including the intra-spec `assembly` pair); the generator evidently only indexes a first-wins or one-per-file basis, so the index's clean bill cannot be used to rule these out. Not verified further — `tools/gen_types_index.py` was not run (read-only constraint).\n- Whether `AwaitingInput`'s `InputRequest` payload is validated by the harness core or by the adapter is stated only in `interop/spec.md:16-17`; no conflicting statement exists to compare, so no finding was raised.\n"
}
```


## ConflictShapesPath

Slice: messages, assembly, structured-output, working-state, telemetry, performance. 18 findings.

```json
{
  "slice": "messages, assembly, structured-output, working-state, telemetry, performance (cross-read: model, tools, build, context, stores, runtime, guards, taint, chains, limits, skills)",
  "findings": {
    "severity": "BLOCKER",
    "claim_a": "`ContextProvider` declared twice in one normative block: first with `Slot() ContextSlot` (with `ContextSlot` enum), then again with only `Provide`; enum `ContextSlot` and enum `Slot` both declare SlotStatic/SlotSession/SlotTurn",
    "a": "openspec/specs/assembly/spec.md:17-28",
    "claim_b": "Second `type ContextProvider interface { Provide(...) }` and `type Slot int` block",
    "b": "openspec/specs/assembly/spec.md:40-50"
  },
  "note": "Same exported name `ContextProvider` with two different method sets, plus two slot enums; docs/design/types.md:50-51 indexes only `ContextProvider`/`ContextSlot` and does not list `Slot`",
  "not_contradictions": "Block order/role model (`Role` has no tool role, results are `RoleUser`; messages:19-25 + messages:180) is consistent everywhere; `Outcome Unknown`/`ReadBack` mapping to `Ref` (messages:183, messages:95-101, working-state:77-79) only conflicts on Get's return type (row 8); `BlobUploader` shape (model:185-187) and blob-cascade/retention (messages:185, stores:507) agree; `ErrBlobTooLarge`→`gohan.blob_too_large` 413 and `*LimitExceededError`→`gohan.limit_exceeded` rows are complete for the messages-owned sentinels; truncated-ToolUse rule is worded identically in messages:189 and structured-output:54; `MemoryStore`/`NotesStore` interfaces, `ErrMemoryStoreRequired` catalog row and `MaxEntries 64`/`TTL 90d` appear only in working-state+stores and do not conflict; `Convention`/`ContentMapping` values and the \"content off by default\" rule are stated once; `ContextBudget` shape and the `TokenBudget.Limit` derivation (model:135-150, model:280, model:373-375) are used consistently by assembly:93; `ToolSpec` field list (tools:33-47) matches tools prose; the performance gate wording (3 rounds, fastest, 5%, cached by SHA) matches performance.regression-gate.",
  "rows": {
    "item": [
      {
        "severity": "BLOCKER",
        "a": "assembly declares `ContextProvider` with method `Slot() ContextSlot` (and enum `ContextSlot`)",
        "line_a": "openspec/specs/assembly/spec.md:17-28",
        "b": "assembly re-declares `ContextProvider` with only `Provide`, plus enum `Slot`, same constant names",
        "line_b": "openspec/specs/assembly/spec.md:40-50"
      },
      {
        "severity": "BLOCKER",
        "a": "`AssembleInput.Providers map[Slot][]ContextProvider` — providers are grouped by slot",
        "line_a": "openspec/specs/assembly/spec.md:37",
        "b": "the `ContextProvider` in that same block has no `Slot()` method, so a provider cannot declare its slot; `notes.New` must return a `gohan.ContextProvider` that working-state calls a `SlotSession` provider",
        "line_b": "openspec/specs/assembly/spec.md:48-50",
        "claim": "slot selection is unrepresentable in the second ContextProvider declaration"
      },
      {
        "severity": "BLOCKER",
        "a": "`type Block interface { isBlock(); Origin() Origin }`",
        "line_a": "openspec/specs/messages/spec.md:48-51",
        "b": "no block struct in the same block (`Text` 53, `Reasoning` 54-58, `Blob` 59-63, `Image` 64, `Audio` 70, `File` 75, `Document` 82, `ToolUse` 90, `ToolResult` 95, `CacheBreak` 102, `Raw` 103, `Compaction` 107) declares `Origin()`; only `Compaction` has an `Origin` *field*",
        "line_b": "openspec/specs/messages/spec.md:53-115",
        "claim": "no block type satisfies `Block`"
      },
      {
        "severity": "BLOCKER",
        "a": "`ToolUse` = `{ID, Name, Args}` only",
        "line_a": "openspec/specs/messages/spec.md:90-94",
        "b": "adapter must convert the provider call to `ToolUse{Executor: ByProvider}`; `Executor` exists only on `ToolSpec`",
        "line_b": "openspec/specs/tools/spec.md:332, 339, 45",
        "claim": "tools sets a field on ToolUse that messages does not declare"
      },
      {
        "severity": "BLOCKER",
        "a": "metric labels are a closed allow-list (`flow, tool, profile, class, release, variant, stage, reason, kind, lifecycle, source, judge`); any other label fails `Build`",
        "line_a": "openspec/specs/telemetry/spec.md:69",
        "b": "the telemetry metric table itself registers `gohan.feedback{name,…}` and `gohan.skill.loaded{name}`, `gohan.router.decisions` by `target`, `gohan.guard.context_rejected` by `provider/notes`, `gohan.schema.upcast{from,to}`",
        "line_b": "openspec/specs/telemetry/spec.md:87, 93, 96, 100, 116",
        "claim": "telemetry's own metrics violate telemetry's own allow-list"
      },
      {
        "severity": "BLOCKER",
        "a": "`Partial` drops the trailing incomplete value",
        "line_a": "openspec/specs/structured-output/spec.md:21",
        "b": "streaming `{\"total\": 12, \"lines\": [{\"sku\": \"A\"` must yield `Total: 12` and one line with `SKU: \"A\"` — the line object is exactly the incomplete trailing value",
        "line_b": "openspec/specs/structured-output/spec.md:45-46",
        "claim": "the partial scenario needs the value the prose drops"
      },
      {
        "severity": "BLOCKER",
        "a": "catalog is closed; `spec:types` fails when a sentinel/typed error declared in a spec has no row, and unmatched errors become `gohan.internal` 500",
        "line_a": "openspec/specs/messages/spec.md:196, 230, 232-236",
        "b": "`ErrStructuredOutput` is the declared failure of `Extract` under `ValidateRepair`/bounded decoding but has no catalog row",
        "line_b": "openspec/specs/structured-output/spec.md:39, 58",
        "claim": "a spec-declared sentinel violates the messages catalog closure"
      },
      {
        "severity": "MAJOR",
        "a": "blob bytes come back from the store: `OutputStore.Get(Ref)` returns the bytes; assembly loads bytes from the store",
        "line_a": "openspec/specs/messages/spec.md:185, 257",
        "b": "`OutputStore.Get(ctx, ref) ([]Block, error)` returns blocks, not bytes; nothing in the port yields the `Data` an `Image`/`Audio`/`File` needs for a provider request",
        "line_b": "openspec/specs/working-state/spec.md:77-79",
        "claim": "shared OutputStore port cannot satisfy the byte-level blob contract"
      },
      {
        "severity": "MAJOR",
        "a": "`Build` fails when a flow can emit a block its model would `Drop` unless the flow opts in via `AllowDrop(File)`",
        "line_a": "openspec/specs/messages/spec.md:188, 292-294",
        "b": "`AllowDrop` is declared nowhere: not a `ToolSpec` field, not a flow option, not in docs/design/types.md",
        "line_b": "openspec/specs/tools/spec.md:33-47, 107",
        "claim": "the fidelity gate has no escapable opt-in, so a Dropping profile is unbuildable"
      },
      {
        "severity": "MAJOR",
        "a": "`gohan.release, gohan.variant, gohan.mode` are on \"all spans, all metrics\"",
        "line_a": "openspec/specs/telemetry/spec.md:60",
        "b": "`mode` is not in the metric label allow-list, so every `gohan.*` metric fails `Build` as specified",
        "line_b": "openspec/specs/telemetry/spec.md:69",
        "claim": "attribute table mandates a metric label the allow-list forbids"
      },
      {
        "severity": "MAJOR",
        "a": "attribute table scopes `gohan.tenant`/`gohan.subject` to the run span and `gohan.tool.*` to tool spans",
        "line_a": "openspec/specs/telemetry/spec.md:59, 63",
        "b": "canonical-keys scenario requires run, model and tool spans to carry the keys of that table",
        "line_b": "openspec/specs/telemetry/spec.md:148",
        "claim": "model and tool spans cannot carry the table they are asserted to carry"
      },
      {
        "severity": "MAJOR",
        "a": "operation names are `invoke_agent, invoke_workflow, chat, execute_tool, retrieval`",
        "line_a": "openspec/specs/telemetry/spec.md:35",
        "b": "the normative span list has no span for `invoke_workflow` and no `retrieval` span",
        "line_b": "openspec/specs/telemetry/spec.md:37",
        "claim": "declared operation name set and span set differ"
      },
      {
        "severity": "MAJOR",
        "a": "budget row budgets taint match for a \"64 KiB untrusted window\", and budgets apply to the `native` runtime with memory stores",
        "line_a": "openspec/specs/performance/spec.md:20, 16",
        "b": "taint must match every string arg against every block in the full `SessionLog` since the last user message, with no cap; the only window bound is the undefined metric `gohan.taint.window_truncated`",
        "line_b": "openspec/specs/taint/spec.md:46, 55",
        "claim": "the 64 KiB window the budget is written against is specified nowhere"
      },
      {
        "severity": "MAJOR",
        "a": "`func Partial[Out any](acc string) (Out, error)` — returns the declared, validated type",
        "line_a": "openspec/specs/structured-output/spec.md:23-26",
        "b": "the same section requires a \"deep-partial view\" whose values are \"never validated\", with unterminated containers closed",
        "line_b": "openspec/specs/structured-output/spec.md:21",
        "claim": "the signature cannot express a deep-partial value of a non-optional struct type"
      },
      {
        "severity": "MAJOR",
        "a": "assembler-side truncation: when history exceeds `TokenBudget.Limit`, oldest turns are dropped whole during assembly",
        "line_a": "openspec/specs/assembly/spec.md:92-94",
        "b": "the only persisted history reduction is the `Compaction` block appended to `SessionLog`; `SessionLog` is stated to stay the single source of truth so `Replay` is deterministic",
        "line_b": "openspec/specs/context/spec.md:9, 48, 57",
        "claim": "an unpersisted second history-reduction path breaks the stated single-source-of-truth replay property"
      },
      {
        "severity": "MINOR",
        "a": "rename-is-config scenario maps `gohan.model.provider`",
        "line_a": "openspec/specs/telemetry/spec.md:164",
        "b": "the canonical model-span keys are `gohan.model.profile`, `.version`, `.endpoint`, `.key_id`; there is no `gohan.model.provider`",
        "line_b": "openspec/specs/telemetry/spec.md:61",
        "claim": "scenario names an attribute key the table does not define"
      },
      {
        "severity": "MINOR",
        "a": "`gohan.memory.evicted{tenant}` is emitted on eviction",
        "line_a": "openspec/specs/working-state/spec.md:74",
        "b": "`tenant` is allowed only with `WithTenantLabel()`, and no wiring point in working-state (or stores' `gohan.session.forked{tenant}`) declares that call",
        "line_b": "openspec/specs/telemetry/spec.md:69",
        "claim": "tenant-labelled metrics are emitted without the required opt-in"
      },
      {
        "severity": "MINOR",
        "a": "`ValidateRepair(max=2)`: \"one repair turn with the validation error is sent; after `max` failures `ErrStructuredOutput` is returned\"",
        "line_a": "openspec/specs/structured-output/spec.md:38-39",
        "b": "with max=2 either one repair turn total (then the second failure cannot be a repair failure) or two (contradicting \"one repair turn\")",
        "line_b": "openspec/specs/structured-output/spec.md:38-39",
        "claim": "repair-turn count is ambiguous between 1 and max"
      }
    ]
  },
  "deferred_excluded": "scenarios.json `deferred_to` scenarios not counted as contradictions: telemetry.langfuse-preset (M4), telemetry.prompt-source-outage (M4), telemetry.feedback-metric-labels (M1), structured-output.constrained (M3), messages.blob-provider-id-reused (M1), all working-state M1 subject-memory scenarios.",
  "unverified": {
    "item": [
      "`AllowDrop` may be intended as an undeclared M0 flow-option; no spec or design doc text exists either way.",
      "Whether `gohan.mode` is meant as a span attribute only — no spec text narrows it.",
      "`InlineBlobBytes` (messages:185) is named with a default but declared in no spec; the numeric 64 KiB coincides with `MaxOutput` 64 KiB (tools:102,128), so this may be a naming collision rather than a conflict — not counted as a contradiction."
    ]
  },
  "escalation": "design call needed for rows 1-2 (which `ContextProvider`/`Slot` shape is canonical) and row 14 (how `Partial` returns a deep-partial value)",
  "specs_reached": {
    "item": [
      "messages",
      "assembly",
      "structured-output",
      "working-state",
      "telemetry",
      "performance"
    ]
  },
  "specs_not_reached": ""
}
```


## ConflictModelPath

Slice: tools, model, decider, build. 13 findings; this lens flattened its own yield, so the entries after the third sit in parallel arrays.

```json
{
  "finding_kind": "contradictions-between-specs",
  "slice": "tools, model, decider, build (cross-checked against chains, structured-output, taint, limits, stores, release, guards where they own the model-call path)",
  "rows": {
    "item": [
      {
        "claim_a": "A 429 sets RetryAfter and retry.RetryAfter waits at least that long before the next attempt on that endpoint",
        "claim_b": "A 429 is never retried on that endpoint; the request goes to the next endpoint immediately",
        "evidence": "model/spec.md:301 vs model/spec.md:407-409 and chains/spec.md:99",
        "severity": "MAJOR"
      },
      {
        "claim_a": "StrictVersion drift fails the call as Permanent so the router moves on / and falls over",
        "claim_b": "ClassPermanent is never retried and never falls back; it always surfaces",
        "evidence": "model/spec.md:284 and model/spec.md:434 vs chains/spec.md:104",
        "severity": "MAJOR"
      },
      {
        "claim_a": "All limiting is keyed by pool, and adapter/redis shares the bucket across every service using the same pool name (two processes with the same pool never exceed the pool limit)"
      }
    ]
  },
  "claim_b": [
    "Breaker, limiter, MaxInFlight, quota pool and hedge budget are keyed by (profile, ProviderCredential.ID)",
    "Exfil is derived from the egress policy: an Untrusted tool whose Allow is only private ranges has Capabilities.Exfil false",
    "MaxEffect caps an Untrusted tool's declared effect at ReadOnly unless explicitly raised in wiring; no wiring raising it for std provider tools is evidenced",
    "ResponseSchema on a profile without Caps.Constrained is a ClassPermanent ModelError at call time, and a dynamic Extra collision fails the call with ClassPermanent",
    "SessionHash affinity without a configured affinity header is a rejected combination (Build error)",
    "An Idle expiry after the first chunk is Permanent for that call and gets no retry or fallback"
  ],
  "evidence": [
    "model/spec.md:256 and stores/spec.md:583-585 vs model/spec.md:278",
    "taint/spec.md:42 vs build/spec.md:67-69 (rule text tools/spec.md:152)",
    "tools/spec.md:327 vs tools/spec.md:124 (scenario tools.untrusted-effect-cap tools/spec.md:288-292)",
    "build/spec.md:59 vs model/spec.md:271 and model/spec.md:274",
    "model/spec.md:271 vs build/spec.md:59 and build/spec.md:83-84",
    "model/spec.md:274 vs build/spec.md:59",
    "chains/spec.md:100 vs model/spec.md:282 and model/spec.md:396-397"
  ],
  "severity": [
    "MAJOR",
    "MAJOR",
    "MINOR",
    "MINOR",
    "MINOR",
    "MINOR",
    "MINOR"
  ],
  "item": [
    {
      "claim_a": "Exfil defaults to true for every Untrusted tool"
    },
    {
      "claim_a": "std/tool/provider ships WebSearch, CodeExec and HostedMCP all Untrusted, with SideEffect for CodeExec and HostedMCP"
    },
    {
      "claim_a": "Validation happens in Build and every flow constructor; nothing is validated lazily per request"
    },
    {
      "claim_a": "The strategy resolver picks std/structured.ToolSchema at Build as the app-side fallback for a profile lacking Caps.Constrained"
    },
    {
      "claim_a": "Imported tool descriptions failing DescribeGuard make Build fail with ErrToolDescription",
      "claim_b": "The normative contract block declares ErrEgressPolicyRequired, ErrManifestDrift, ErrToolCollision, ErrToolName, ErrToolSetDrift only; the DescribeGuard field is typed Guard and no ErrToolDescription is declared anywhere in openspec (not evidenced)",
      "evidence": "tools/spec.md:281 vs tools/spec.md:71-84 and tools/spec.md:120",
      "severity": "MINOR"
    },
    {
      "claim_a": "AffinityKey and Priority that no adapter maps are ignored with a Build warning"
    },
    {
      "claim_a": "Timeouts are ClassTransient, which retries with backoff and then falls over after retries"
    }
  ],
  "claim_a": "Contradicting form: the flow constructor returns an error naming flow, profile and strategy for the same situation",
  "checked_clean": "Checked and found clean: ModelRequest/ModelOptions/ModelProfile/Caps/ErrorClass/ModelError/TokenBudget/ContextBudget/Timeout defaults/LatencyClass/KeyMode/Pricing/CacheMode/CompactionMode field sets and enum members in model; ToolSpec/Effect/RiskTier/Trust/ToolPolicy/EgressPolicy/Executor and the four declared sentinels in tools; Strategy table (Assembler, StructuredOutput, Router, AffinityKeyStrategy, Limiter, RetryPolicy, ContextPolicy, ResumeStrategy) against model's AffinityKeyStrategy and build's rejected-combination list; decider's Decision/Decider, Confidence==1 for rules, gate-Ask / guard-Block / router-Static error defaults against permission/spec.md:28 and guards/spec.md:50,70 (no contradiction); the tool error mapping (Go error to Failed(Permanent), Retryable marker, SideEffect timeout to Unknown) against tools scenario tools.default-timeout and chains/spec.md:70,74; Stack.Manifest() return type against release/spec.md:36-48 (Tools map[string]string is consistent with the tool hash list in tools/spec.md:124); Sunset/NoticeWindow against lifecycle/spec.md:24-26; ProviderToolCap approval against tools/spec.md:330; the 429 status-to-class fixture mapping and the context-overflow and content-policy rows.",
  "not_evidenced": {
    "item": [
      "ErrToolDescription",
      "an explicit MaxEffect raise for std/tool/provider tools"
    ]
  },
  "design_call": "Two need a decision, not a fix: which limiter key is authoritative (pool vs profile+credential) and whether StrictVersion/Version-drift may fail over under chains' ClassPermanent-never-fails-over rule",
  "reached_specs": {
    "item": [
      "model",
      "tools",
      "decider",
      "build",
      "chains",
      "structured-output",
      "taint",
      "limits",
      "stores",
      "release",
      "guards",
      "permission",
      "runtime",
      "agui",
      "identity",
      "messages",
      "redaction",
      "subflows",
      "lifecycle"
    ]
  }
}
```


## ConflictStatePath

Slice: stores, recovery, limits, runtime, streams. 13 findings.

```json
{
  "slice": "stores / recovery / limits / runtime / streams — run state, leases, sequence numbers, turn and cost accounting, timeouts, retention",
  "rows": {
    "item": [
      {
        "severity": "MAJOR",
        "claims": "runtime: every turn appends the assistant message at model completion, then batch results in a second Append | recovery: a turn with no or only ReadOnly calls uses ONE Append carrying message+results together",
        "evidence": "runtime/spec.md:109 + runtime/spec.md:110 + runtime/spec.md:189 (runtime.append-before-tool) vs recovery/spec.md:18 + recovery/spec.md:26"
      },
      {
        "severity": "MAJOR",
        "claims": "Done(end_turn) and Done(max_turns) | Done.Reason is StopReason whose only members are completed/suspended/limit/guard_blocked/cancelled/failed/shadow_suspended/handed_off",
        "evidence": "runtime/spec.md:291 (runtime.plain-answer) + runtime/spec.md:306 (runtime.max-turns) vs streams/spec.md:17-28 + streams/spec.md:66"
      },
      {
        "severity": "MAJOR",
        "claims": "LimitExceededError.Limit = \"MaxCost\" on the tree-budget scenario | the same error type carries Limit = \"cost\" on the hard-cost scenario",
        "evidence": "limits/spec.md:67 (limits.cost-accumulates) vs limits/spec.md:74 (limits.hard-cost-abort); same field at limits/spec.md:37-40"
      },
      {
        "severity": "MAJOR",
        "claims": "Stale lists only state Running, and Recover reaches stale runs only via Stale -> Reclaim | a crash after Resuming leaves a Resuming run whose input Recover reads back and re-drives, and only Running and Resuming are ever reclaimed",
        "evidence": "stores/spec.md:306 (Stale = state Running) + recovery/spec.md:20 (Stale -> Reclaim) vs stores/spec.md:305"
      },
      {
        "severity": "MAJOR",
        "claims": "FeedbackRecorded is appended to the finished run with the next Seq, keeping Seq gapless 1..N | Done is the last event of a run and a run has a single writer under its lease",
        "evidence": "streams/spec.md:201 (streams.feedback-recorded-event) + streams/spec.md:196 vs runtime/spec.md:194 (runtime.done-after-finish) + streams/spec.md:118"
      },
      {
        "severity": "MAJOR",
        "claims": "the last delivered Seq is reported in the Runs record and in Inspect | the normative Run struct has no Seq field and Inspect reads the Run record from stores",
        "evidence": "streams/spec.md:94 + recovery/spec.md:24 vs stores/spec.md:126-138"
      },
      {
        "severity": "MAJOR",
        "claims": "a Verify-reconciled journal entry becomes Succeeded or Failed | the journal EntryState enum has only Reserved and Completed",
        "evidence": "tools/spec.md:130 + tools/spec.md:361 (tools.verify-reconciles-unknown) vs stores/spec.md:95-98 + stores/spec.md:102-109"
      },
      {
        "severity": "MAJOR",
        "claims": "an Entry with Outcome: Unknown is inspected alongside state Reserved | Entry has no Outcome field; Unknown exists only on ToolResult inside Entry.Result",
        "evidence": "stores/spec.md:300 + stores/spec.md:405-407 vs stores/spec.md:102-109"
      },
      {
        "severity": "MAJOR",
        "claims": "an unsigned Finish+Suspend transaction is retried by the claim/ack outbox, delivering the notice exactly once | notices are delivered at least once, retried, and deduped only by consumer idempotency key",
        "evidence": "stores/spec.md:433 (stores.notice-written-with-finish) vs streams/spec.md:125"
      },
      {
        "severity": "MAJOR",
        "claims": "Finish with pending steers drains and runs one more turn (unconditional) | MaxTurns aborts the run as not resumable, and Done(max_turns) is emitted after exactly MaxTurns model calls",
        "evidence": "stores/spec.md:437 vs limits/spec.md:48 + runtime/spec.md:306 (runtime.max-turns)"
      },
      {
        "severity": "MAJOR",
        "claims": "run defaults are MaxTurns 20 / MaxToolCalls 50 / MaxWallClock 10m | the Interactive preset installed by std.Interactive() carries MaxTurns 6 / MaxToolCalls 20 / MaxWallClock 60s",
        "evidence": "limits/spec.md:48 vs limits/spec.md:32 + limits/spec.md:50"
      },
      {
        "severity": "MAJOR",
        "claims": "transports set a per-event write deadline equal to ConsumerStall | BatchLimits leaves ConsumerStall at 0, and 0 is defined as stall-preemption disabled",
        "evidence": "streams/spec.md:104 vs limits/spec.md:34 + streams/spec.md:102"
      },
      {
        "severity": "MAJOR",
        "claims": "a preempted Suspended session never ages, so its Checkpoints are never Conversation-purged | without PreemptedLister a preempted run expires with its checkpoint, and Consume then returns ErrTokenExpired",
        "evidence": "stores/spec.md:507 vs stores/spec.md:306 + stores/spec.md:299"
      },
      {
        "severity": "MINOR",
        "claims": "DeleteSession with a live lease fails with ErrRunActive | DeleteSession under a hold fails with ErrSessionHeld, with no stated precedence between the two",
        "evidence": "stores/spec.md (stores.delete-cascades) vs stores/spec.md:524 (stores.hold-blocks-delete-and-purge) + stores/spec.md:509"
      },
      {
        "severity": "MINOR",
        "claims": "leasing is refreshed by the harness every ttl/3 | Runs.Heartbeat is folded into the per-turn store writes where the store supports it",
        "evidence": "stores/spec.md:306 + runtime/spec.md:119 vs recovery/spec.md:26"
      },
      {
        "severity": "MINOR",
        "claims": "MaxCost is required when Pricing is configured | MaxCost defaults to 0 = unlimited and Build only warns when it is unset outside Batch",
        "evidence": "limits/spec.md:48 vs limits/spec.md:50"
      },
      {
        "severity": "MINOR",
        "claims": "ArchivedRetention (default 180 days) is the threshold for archived sessions and WithOperationRetention governs operation retention | RetentionPolicy is a closed three-field struct with neither knob, and the prose never states whether they are Build or Build-time-validation inputs",
        "evidence": "stores/spec.md:308 + stores/spec.md:304 vs stores/spec.md:38-42"
      }
    ]
  },
  "checked_clean": "Lease exclusivity and ErrRunActive across Runs.Start / ForkSession / DeleteSession (stores:306,343-346,484); the two-clocks split (store time for lease, staleness, checkpoint expiry, journal, operation retention; monotonic harness time for MaxWallClock, ToolSpec.Timeout, HeartbeatEvery, ReaperEvery) agrees across stores:309, limits:87-89 and streams:94-106; retention arithmetic in stores.purge-respects-pinned-and-archived is consistent with ArchivedRetention 180d (200d purged, 100d kept) and Pinned/Archived/held skips; error-tuple protocol (streams:82-86) is compatible with the pre-Drive ordering in runtime:95-104 and with ErrRunActive as a sole pre-flight tuple; Seq gapless 1..N agrees with Detached coalescing, Attach(afterSeq) and SSE id; single-use checkpoint tokens and the Reclaim race / Stale-by-store-clock pair; notice thinness (streams:125,128-129) against audit no-content (stores:376-379); journal key pinning and fingerprint-after-compaction (stores:300, 383-386).",
  "not_evidenced": "ArchivedRetention and WithOperationRetention as declared identifiers — not evidenced in any of the five specs; only referenced in prose. Harness time is monotonic-only, for budgets and timeouts (`RunLimits.MaxWallClock`, `ToolSpec.Timeout`, `ModelProfile.Timeout`, `HeartbeatEvery`, `ReaperEvery`) via `time.Since` and `context.WithTimeout`; no rule compares a harness instant to a stored one — verified at stores/spec.md:309.",
  "coverage": "All 5 assigned specs read in full (stores 647 lines, runtime 328, streams full, recovery 56, limits full). All 58 stores scenarios and every runtime/limits/streams/recovery scenario ID cross-checked against openspec/scenarios.json for deferred_to: every scenario cited above is NOT deferred except limits.provider-calls-count (M1) and stores.retention-by-partition (M3), which are not used as evidence. docs/design/types.md was not consulted (reported generated); contradictions above are prose-rule conflicts, not identifier collisions."
}
```


## ConflictRequestPath

Slice: flow, suspension, identity, permission, chains, guards. findings, including three blockers.

```json
{
  "slice": "flow, suspension, identity, permission, chains, guards",
  "scenarios_checked": {
    "scenarios_checked": {
      "note": "openspec/scenarios.json carries no deferred_to key for any scenario in this slice (searched flow./identity./suspension./permission./chains./guards.), so no row below is excused by milestone deferral.",
      "count": "flow 23 + identity 23 + suspension 6 + permission 15 + chains 19 + guards 10 = 96 scenario IDs in slice; all present in scenarios.json"
    },
    "findings": {
      "item": [
        {
          "claim_a": "`MaxPendingApprovals` is a field of the per-run `RunLimits` struct",
          "claim_b": "`MaxPendingApprovals` is enforced \"per subject and per tenant, default 20\"",
          "file_a": "openspec/specs/limits/spec.md:28",
          "file_b": "openspec/specs/permission/spec.md:98",
          "severity": "BLOCKER",
          "why": "A field carried in RunLimits is scoped to one run's limits; a per-subject/per-tenant pending-approval count spans runs and sessions, which a per-run value cannot express. The two scopes also produce different answers for the flood test."
        },
        {
          "claim_a": "`MaxPendingApprovals` default is 20",
          "claim_b": "`InteractiveLimits`, `AgenticLimits` and `BatchLimits` leave the field unset (zero value)",
          "file_a": "openspec/specs/permission/spec.md:98",
          "file_b": "openspec/specs/limits/spec.md:32-34",
          "severity": "BLOCKER",
          "why": "With the shipped presets the field is 0, so under the literal reading every `Ask` is immediately a limit violation; under the intended reading the presets must set 20. Both readings cannot hold for the same struct."
        },
        {
          "claim_a": "Gate `Ask` suspends with `HumanApproval` and `payload = pending ToolUse`",
          "claim_b": "Gate `Ask` suspends with an `ApprovalRequest` as payload (risk, fingerprint, ArgOrigins, DiffFromLast, eligibility)",
          "file_a": "openspec/specs/suspension/spec.md:81",
          "file_b": "openspec/specs/permission/spec.md:94",
          "severity": "BLOCKER",
          "why": "`SuspendError.Payload` is `any` and both specs are normative prose about the same suspension. A bare `ToolUse` carries no `Risk`, `Fingerprint` or `Eligible`, so the approval-policy check on Resume and `suspension.approval-notice-delivered` (Inspect must yield the ApprovalRequest) cannot be implemented from the payload suspension states."
        },
        {
          "claim_a": "Resume tokens are single-use; a consumed token yields `ErrTokenConsumed` and the tool does not run again",
          "claim_b": "With `Quorum > 1` the token stays valid until quorum, and each partial approval is recorded",
          "file_a": "openspec/specs/suspension/spec.md:90",
          "file_b": "openspec/specs/permission/spec.md:96",
          "severity": "MAJOR",
          "why": "A `Consume`-on-first-Resume implementation and a resume-many-times quorum are different token state machines. `permission.quorum-two-approvers` (spec line 148) needs the second `Resume` to succeed on the same token, while `suspension.token-reuse` (spec line 105) needs it to fail."
        },
        {
          "claim_a": "An ineligible approver gets `ErrApproverNotEligible`, the token stays unconsumed, and audit records `approval_refused`",
          "claim_b": "The same refusal records `approval_refused{reason=separate_from_originator}`",
          "file_a": "openspec/specs/permission/spec.md:96",
          "file_b": "openspec/specs/permission/spec.md:133",
          "severity": "MINOR",
          "why": "Not blocking on its own: both fit one reason enum, but the contract prose leaves the reason vocabulary open while the scenario pins one value, so the audit reason for `RiskMedium` scope failures is unspecified."
        },
        {
          "claim_a": "`ApproveScope(ttl time.Duration) ResumeInput` — one parameter",
          "claim_b": "Gate prose and scenarios call `ApproveScope(op, 2h)` — a principal plus a TTL",
          "file_a": "openspec/specs/permission/spec.md:91",
          "file_b": "openspec/specs/permission/spec.md:94",
          "severity": "MAJOR",
          "why": "Two-arg call sites do not compile against the declared signature. Compounded by identity spec line 86, which states `ApproveScope()` takes no principal, so the extra `op` argument has no rule."
        },
        {
          "claim_a": "Rules for a tool returning `gohan.SuspendTool(...)` all treat the tool call as suspended-and-resumable",
          "claim_b": "`HumanHandoff` via the same `gohan.SuspendTool(HumanHandoff, ...)` call ends the run and creates no token",
          "file_a": "openspec/specs/suspension/spec.md:82",
          "file_b": "openspec/specs/suspension/spec.md:86",
          "severity": "MAJOR",
          "why": "One constructor with two incompatible outcomes. `tools` spec line 132 says `SuspendError` passes through the tool boundary, which is false for HumanHandoff: the run ends `StopHandedOff` instead. `HumanHandoff` is also a `SuspendReason` member (suspension spec line 29) despite never producing a suspension."
        },
        {
          "claim_a": "`TakeOver` expires pending approval tokens with `not_executed: handed_off`",
          "claim_b": "The gate's only non-approval terminal outcomes are expiry-by-clock and `Reject()`; the pending call on expiry becomes `Failed(Permanent)`",
          "file_a": "openspec/specs/flow/spec.md:96",
          "file_b": "openspec/specs/permission/spec.md:98",
          "severity": "MINOR",
          "why": "`not_executed: handed_off` is a third disposition of a pending approval that neither the `SuspendReason` enum nor the `ApprovalRequest`/expiry rules define, and `suspension.approval-notice-delivered` (spec line 117) has a client still fetching a live `ApprovalRequest` by run id."
        },
        {
          "claim_a": "`ErrNoPrincipal` is declared in the flow capability spec",
          "claim_b": "Every `Invoke`/`Send` without a principal must return `ErrNoPrincipal`, with `AllowAnonymous` as the opt-out",
          "file_a": "openspec/specs/flow/spec.md:21",
          "file_b": "openspec/specs/identity/spec.md:82",
          "severity": "MINOR",
          "why": "Declaration and behavioural rule sit in different capabilities with no stated owner. Not a compile conflict, but the sentinel's owning spec is undetermined while both are marked source of truth."
        },
        {
          "claim_a": "`permission.Verdict` is `Allow` / `DenyVerdict` / `Ask`; `suspension.ApprovalVerdict` is `VerdictApprove` / `VerdictReject` / `VerdictEdit`",
          "claim_b": "`Approval` records `Verdict ApprovalVerdict` while `ResumeInput.Verdict` is set by `Reject()` to the rejection verdict",
          "file_a": "openspec/specs/permission/spec.md:19-23",
          "file_b": "openspec/specs/identity/spec.md:40",
          "severity": "MINOR",
          "why": "Two distinct enums both named `Verdict` in the approval record and the gate result; `ApproveScope()` sets no `ApprovalVerdict` member at all even though permission spec line 96 lists it as an approval-bearing input. The scope-grant verdict value is unspecified."
        }
      ]
    },
    "not_evidenced": {
      "item": [
        "`suspension.scheduled` claims `Waker.Schedule(token, t)` is called exactly once (suspension spec line 129) while `permission.escalation-targets` re-suspends and passes an escalation target to `Waker.Schedule` (permission spec line 153); no spec states whether escalation on the same token is a second `Schedule` call or a reschedule. Not a contradiction, an unstated case.",
        "`chains` ordering constraint \"`Hedge` must be outside `Fallback` and inside `Router`\" (chains spec line 55) is consistent with the printed model order (router → hedge → fallback, chains spec lines 84-86). Checked, clean.",
        "`guards` `Origin` assignment, `Windowed` 64-token default, and `Done(guard_blocked)` / `StopGuardBlocked` were cross-read against messages spec line 182 and streams spec lines 20-27. Checked, clean.",
        "Grants-not-inherited-on-fork (`permission` spec line 122) vs `stores` ForkSession inheritance (stores spec line 307) agree: owner yes, grants no. Checked, clean.",
        "`identity.steer-root-only` (identity spec line 118) vs `flow` steering rule (flow spec line 107) and `runtime` drain rule (runtime spec line 121) all agree that a child session id yields `ErrRunNotActive`. Checked, clean."
      ]
    },
    "coverage": {
      "checked": "All six specs read in full: flow (242 L), identity (224 L), suspension (134 L), permission (178 L), chains (253 L), guards (130 L). Truncated long lines recovered in full via line-targeted reads. Cross-reads into limits, messages, streams, stores, runtime, tools, telemetry, engines, agui, interop.",
      "clean": "Shared types verified free of field/signature divergence across this slice: `SuspendError{Token,Reason,Payload,WakeAt}`, `ResumeInput{Approver,Verdict,Args,Data,Reason}`, `ApprovalVerdict`, `Principal{Subject,Tenant,Scopes}`, `Credential`, `SessionOwner`, `Approval{Approver,Verdict,At}`, `RunInfo` (13 fields, no second declaration in slice), `GuardStage` (5 members), `GuardAction` (3), `GuardVerdict`, `GuardInput`, `GuardBlockedError`, `StepError`, `Step[M]`, `StepKind` (11 members), `Waker`, `ToolInvocation`, `Eligibility`, `ApprovalRequest`, `ApprovalPolicy`, `Grant`, `ApprovedVia`, `ExpiryAction`. Scopes grammar: `session:read`/`session:write`/`session:hold`/`session:control` used consistently and `TakeOver` scope claim agrees across flow spec line 96 and permission spec line 128. Suspension reasons: all 9 `SuspendReason` members referenced consistently. Suspend-not-resumed set (`HumanHandoff` → `StopHandedOff`) consistent across suspension spec line 86 and flow spec line 192.",
      "not_reached": "None."
    },
    "escalation": "Design call needed on the quorum token state machine and on the owner of `ErrNoPrincipal` → zarchitect."
  }
}
```

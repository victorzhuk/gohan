# Design

## Context

Seven capability specs leave security-critical error paths or posture rules to architecture prose. `docs/design/architecture.md` states the decider asks on error, the redaction placements, the container anchor posture and the egress literal allow; the specs state none of it normatively. A conformance-only implementation can therefore return Allow on a decider error, continue unredacted on a detector outage, keep capabilities on the workload container, and exempt a private destination through a hostname glob, while passing every registered scenario.

The deltas make the shipped behavior normative and give each rule a scenario that fails when the rule is removed. Most rules describe behavior the core and adapters already implement; the change binds them, it does not redesign the runtime.

## Goals and non-goals

Goals:

- Every fail-closed rule has a requirement and a registered scenario bound to a subtest.
- The taint matching representation (post-redaction text) is decided, not inferred.

Non-goals:

- No change to port shapes, `types/` identifiers or adapter APIs.
- No behavior change where the shipped implementation already satisfies the rule.
- No new redaction detector, sandbox backend or egress transport.

## Decisions

### 1. Decider error resolves to Ask

`core/permission/gate.go` already returns `Ask` when the decider errors and when confidence falls under the minimum. The requirement freezes that outcome: a broken decider suspends with an `ApprovalRequest` and the run resumes through the ordinary approval path. A gate without a decider keeps the effect defaults (`ReadOnly`/`Idempotent` pass, `SideEffect` asks) — the error path must not be read as "no decider means ask always".

Threatened invariant: a decider outage must not widen authority. Implementation: requirement text plus `permission.decider-error-asks`.

### 2. Redaction failure fails the turn

A Detector, Redactor or KeySource error stops the turn before unredacted content reaches the placement's next consumer. The alternative — continue with raw content — converts a dependency outage into a disclosure. The requirement names the placement so the failure is bounded: input redaction fails before the model call, store redaction fails before the write, output redaction before delivery.

Placement order stays as shipped: input, store, output, telemetry capture. Telemetry capture runs after redaction at its placement, so spans and events carry redacted content only. Evaluation exports carry no raw entity and no injected secret.

Threatened invariant: raw entities and injected secrets never reach a store, a model, an export record or a span.

### 3. Erasure covers notes

Notes are subject-scoped records derived from conversation content. Leaving them on erasure leaves the subject's data behind, so the erasure enumeration gains notes and its existing scenario name notes.

### 4. Anchor posture is a sandbox requirement

The workload container drops all capabilities, adds none, enables no-new-privileges, overrides the image entrypoint and disables inherited healthchecks. Network policy administration targets the anchor control channel, never public `Workspace.Exec`, and each operation is bounded to ten seconds. Promotion matters because the posture is a security control that conformance should reject when absent.

### 5. Egress literal allow is exact

An `Allow` entry exempts only an exact literal destination address. Hostname patterns, globs and CIDRs never exempt a resolved private, loopback, link-local or shared-address destination. Refusal happens before dialing, so a refused destination produces no connection attempt. The CGNAT range stays denied except for an exact literal listed in `Allow`.

Threatened invariant: the private-address deny cannot be widened by a pattern match or a rebinding.

### 6. Taint survives transforms and matches post-redaction text

ClearToolResults, compaction, notes read-back and memory read-back preserve origins. Blocks produced by these transforms inherit their origins rather than starting clean, otherwise a tainted result launders into an untainted history entry.

Taint matching runs over the post-redaction text the model receives. The model never sees raw entities, so a matcher over raw text would miss every pseudonymized entity and silently disable the exfiltration check whenever a Redactor is active. Matching the delivered text keeps the check aligned with what the model could exfiltrate.

Window truncation increments `gohan.taint.window_truncated` once per truncation.

The seam: `turnConfig` carries the run's `Redactor` and `RedactionPolicy`, and the effect context installs the window through the same redaction gate as the model request (nil redactor or empty detectors keep the raw window). Tokens derive from the tenant key, session and canonical value, so redacting the window separately yields exactly the tokens the request carries; the returned map is discarded because the request path remains the single map writer. A redaction error while building the window fails the turn.

ADR-0188 records this decision with the alternative (matching both representations) and its cost.

### 7. Guard error blocks

A guard error at any stage is `Block` at that stage: the output is cut and `*GuardBlockedError` carries the stage and reason. One shared converter at the consultation boundary produces the block, passes an existing `*GuardBlockedError` through unchanged, and keeps the cause reachable: the error type gains an optional `Err` field with `Unwrap`, so the zero value and the error text stay identical. Compaction treats a consultation error exactly like a Block verdict and never persists the summary.

### 8. Anonymous mode bypasses only principal presence

`AllowAnonymous` waives the principal-presence check alone. Store authorization, owner checks and scope checks still run, so an anonymous run cannot read another owner's session.

## Risks

- **Tests that pass without the rule.** A scenario that only asserts the happy path leaves the fail-closed rule unproven. Each new scenario forces the error or posture condition (erroring decider, erroring detector, missing Docker daemon aside) and asserts the refusal, not the success.
- **Docker conformance skips.** `sandbox.workload-posture-locked` and `sandbox.admin-channel-isolated` need a daemon. They follow the suite's existing named-prerequisite skip and run in the full leg; the short suite reports the skip rather than a pass.
- **Double matching in taint.** Matching post-redaction text can miss an entity that redaction removes entirely. The requirement keeps the exfiltration check firing on the pseudonymized content, which is what the model can leak.
- **Placement coupling.** Making redaction errors fatal turns a detector outage into a turn failure. That is the intent, and the failure stays visible and retryable through the ordinary recovery path.

## Verification

- Each new scenario ID is a subtest named exactly by its ID in the owning module.
- `task spec:types` regenerates the identifier index; no identifier is added, so the index must be unchanged.
- `task spec:gate` proves registration and binding for the new IDs.
- `task lint` and `task test` cover the change floor; `openspec validate governance-fail-closed --strict` covers artifact shape.
- The Docker scenarios run under the full leg with a daemon available.

## Open questions

None. The matching-representation decision is settled above and ratified by ADR-0188.

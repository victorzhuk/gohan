# ADR-0188: Governance fail-closed contracts

Date: 2026-11-13
Status: Proposed (`governance-fail-closed`)
Supersedes: none

## Context

The governance audit of 2026-11-12 read `openspec/specs` against `docs/design/architecture.md` and found seven security rules that live only in prose or are silent on error paths. Each is a rule a conforming implementation can omit while every registered scenario still passes:

- The decider error outcome is stated in `architecture.md` and again as a code comment in `core/permission/gate.go`, but `permission/spec.md` never contracts it, so error-as-Allow violates no MUST.
- `redaction/spec.md` gives Detector, Redactor and KeySource error returns without ruling on their outcome; continuing unredacted is permitted by silence.
- Telemetry capture is a redaction placement with no scenario, and no rule forbids raw entities or injected secrets in exported evaluation signals.
- Capability drop, add-none, no-new-privileges, entrypoint override and the ten-second administration bound appear only in `architecture.md`; a conformance test can pass while the workload keeps capabilities.
- `tools/spec.md` references an "existing explicit literal allow exception" that no spec defines, so a hostname glob could be read as exempting a private resolution.
- No scenario binds taint survival across ClearToolResults, compaction or notes read-back, and no rule states whether taint matching reads raw or post-redaction text.
- `guards/spec.md` contracts Pass, Rewrite and Block only, so a detector error can pass injection or PII content through the guard stage.

The audit is the evidence; no incident is required. The threatened invariants are the ones the specs already rely on: a decider outage must not widen authority, and raw entities and injected secrets must not reach a store, a model, a span or an export record.

## Decision

1. **Decider error resolves to `Ask`.** A decider error suspends the call with an `ApprovalRequest`; it never widens to `Allow`. A gate without a decider keeps the effect defaults. Shipped code already returns `Ask` in `core/permission/gate.go`; the requirement and `permission.decider-error-asks` make the outcome enforceable.
2. **Redaction failure fails the turn.** A Detector, Redactor or KeySource error stops the turn before unredacted content passes that placement. Every placement propagates the error today (`core/conversation.go`, `core/native_effects.go`, `core/lifecycle_redaction.go`); the requirement forbids changing that to continue.
3. **Telemetry capture and evaluation export carry no raw content.** Telemetry capture runs after redaction at its placement; exported evaluation signals carry references and no raw entity or injected secret.
4. **Erasure covers notes.** Subject erasure deletes the subject's notes and note-derived records with the subject's other records. `core/erasure.go` already routes the notes store through `stores.MemoryEraser`.
5. **The workload anchor posture is a sandbox requirement.** A networked workload drops all capabilities, adds none, enables no-new-privileges, overrides the image entrypoint and disables inherited healthchecks. Network policy administration targets the anchor channel, never public `Workspace.Exec`, and each operation is bounded to ten seconds. The trusted anchor keeps `NET_ADMIN` because it is the administration channel; only the workload container carries the zero-capability posture.
6. **The egress literal allow exception is exact.** An `Allow` entry exempts only an exact literal destination address. A hostname, glob or CIDR never exempts a resolved private, loopback, link-local or `100.64.0.0/10` destination, and refusal happens before dialing. `std/egress` already implements this for the direct dial path.
7. **Taint survives content transforms and matches post-redaction text.** ClearToolResults, compaction, notes read-back and memory read-back preserve origins. Taint matching evaluates the post-redaction text the model receives. The alternative — matching raw text — misses every pseudonymized entity, because the model never sees the raw entity and can only exfiltrate the pseudonym; an active Redactor would silently disable the exfiltration check. Matching both representations would keep the raw entity in the comparison path without improving detection, since arguments carrying the raw entity can only come from a caller that never saw it.
8. **A guard error blocks at its stage.** The output is cut and `*types.GuardBlockedError` carries the stage and reason.
9. **Anonymous mode bypasses only the principal-presence check.** Store authorization, owner checks and scope checks still run; an anonymous run cannot read another owner's session.

## Consequences

- The seven specs gain requirements with scenario IDs bound to subtests, so removing a fail-closed path fails the gate rather than passing silently.
- Detector and redactor outages now fail turns and runs visibly. That is the intent: an outage is a failure, not a disclosure.
- Taint matching needs the window in the representation the model receives, so the window is built from redacted blocks when a redactor and detector policy are configured. A redaction error while building the window fails the turn.
- Notes read-back blocks carry an origin so taint propagates through them instead of starting clean.
- Guard call sites convert detector errors into `*types.GuardBlockedError`; the error chain keeps the cause for callers that inspect it.
- The Docker posture scenarios need a daemon and follow the suite's named-prerequisite skip in daemon-less runs; they run in the full leg.
- Two known gaps remain outside this decision: `std/redact` tokenizes only top-level `Text` blocks, so tool-result and document content reaches the model unredacted, which contradicts redaction placement 1 and limits the taint window seam to the blocks redaction actually rewrites; and the proxied egress dial path denies an allowlisted CGNAT literal, and the Docker network layer mirrors the private-address set without `100.64.0.0/10`. Neither contradicts the requirements of this decision, and both are recorded for a follow-up repair.

## References

- `docs/design/architecture.md` — decider error, redaction placements, anchor posture, evaluation signals.
- `docs/design/compatibility.md` — frozen ports; `GuardBlockedError` gains an optional cause without changing existing fields.
- `core/permission/gate.go`, `core/conversation.go`, `core/native_effects.go`, `core/lifecycle_redaction.go`, `core/erasure.go`, `core/telemetry.go`, `std/taint/taint.go`, `std/notes/notes.go`, `std/guard/output.go`, `std/egress/client.go`, `adapter/docker/workspace.go`, `adapter/docker/sandbox.go`.

# Proposal

## Why

The security audit of the governance specs (2026-11-12, audit mode) verified that several security-critical behaviors live only in architecture prose or are silent on error paths, leaving them unenforceable by any scenario gate:

1. **Decider error fail-closed absent** (High). `permission/spec.md` states the gate order and defaults (:28, :102, :191) but never contracts decider-error behavior. "A decider error asks rather than widening" lives only in `docs/design/architecture.md:74`. A reader can implement error-as-Allow without violating any permission MUST.
2. **Redaction failure paths unbounded** (Medium). `redaction/spec.md:89-104` gives Detector/Redactor/KeySource error returns but no requirement governs failure; continuing unredacted on a detector or key outage is permitted by silence.
3. **Telemetry capture and eval export unbound** (Medium). Redaction placement 3 (telemetry capture) has zero scenarios, and no rule forbids raw entities or injected secrets in exported evaluation signals (`architecture.md:181` keeps evaluation signals in the export records).
4. **Sandbox anchor posture only in prose** (Medium). Cap-drop, add-none, no-new-privileges, entrypoint override, anchor-only administration and the ten-second bound appear in `architecture.md:183-187` but not in the sandbox spec; an adapter can pass conformance while retaining capabilities.
5. **CGNAT literal-allow exception undefined** (Medium). `tools/spec.md:230-231` carries the 100.64.0.0/10 rule and references "the existing explicit literal allow exception" that no spec defines; the bypass can be read broadly.
6. **Taint anti-laundering and redaction interaction unbound** (Medium). No scenario binds ClearToolResults/compaction/notes read-back origin preservation or `gohan.taint.window_truncated`; no spec states whether taint matching compares raw or post-redaction text, so an active Redactor can silently disable exfil matching.
7. **Guard error path unbounded** (Medium). `guards/spec.md` contracts Pass/Rewrite/Block only; a guard error may pass injection or PII content through the detection layer.
8. **Low**: `identity.AllowAnonymous` bounds have no binding scenario; the redaction erasure enumeration omits notes.

## What Changes

Add fail-closed requirements and binding scenarios: decider error resolves to Ask; redaction failure fails the turn before unredacted content passes the placement; telemetry capture and eval export carry no raw content; erasure covers notes; the workload anchor posture becomes a sandbox requirement; the egress literal-allow exception is defined normatively (exact literal address only, globs never exempt, refusal before dialing); taint survives content transforms and matches post-redaction text; guard error blocks at its stage; anonymous mode bypasses no store authorization.

The taint matching representation (post-redaction text) is a contract decision this change proposes: the model receives post-redaction text, so matching raw text would miss every pseudonymized entity. ADR-0188 ratifies it at admission.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `permission`, `redaction`, `sandbox`, `tools`, `taint`, `guards`, `identity`: added requirements and scenarios (deltas). Existing requirement text is unchanged except the redaction erasure enumeration, which gains notes.

## Impact

Spec deltas plus scenario registration at admission (ADR-0171) and subtest binding: reuse existing covering tests where present, write focused enforcement tests where absent (security-boundary residual risks per the testing strategy). Shipped behavior already implements several of these paths (decider-error-asks per `architecture.md:74`); the change makes them enforceable, it does not alter runtime behavior. ADR-0188 records the decisions.

Dependencies: admit after `repair-contract-drift` (shared `sandbox` and `identity` files). Independent of `scenario-record-reconciliation` except registry writes, which are additive.
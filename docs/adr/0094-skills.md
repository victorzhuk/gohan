# ADR-0094: Skills under tool controls

Status: accepted · Origin: grill round 20 (2026-09-29)

## Decision

`std/skills` over a `SkillSource` port (`List`, `Load`, `Resource`) with a filesystem implementation of the SKILL.md layout. L1 metadata is a sorted `SlotStatic` catalog hashed into the pinned manifest; L2 bodies load through the `ReadOnly` meta-tool `load_skill` and enter history as guarded, re-fetchable tool results with `Origin{OriginTool, "skill/<name>"}`; L3 resources through `read_skill_resource` with `OutputStore` overflow. Skills outside the service's repository are `Untrusted`: instructions-only, no scripts, descriptions guarded. Scripts run only via the `sandbox` capability and only for `Trusted` skills. `RequiredTools` is validated at `Build`; loading never widens the tool set. Hash drift fails closed. `Explain` and `AuditLog` record loads. No LLM-based vetting in core.

## Context and evidence

SKILL.md is the portable unit of procedural knowledge across harnesses, and a measured attack surface (a quarter of community skills vulnerable, scripts doubling the odds). gohan already had every control needed — trust tiers, effect caps, description and result guards, pinned manifests, sandbox — for tools; skills reuse them instead of adding a parallel system.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

New capability `skills`; `telemetry` gains `gohan.skill.*`; `assembly` layout names the catalog. Verification pipelines (static/semantic/behavioral gates) belong to evals/CI, not the request path.

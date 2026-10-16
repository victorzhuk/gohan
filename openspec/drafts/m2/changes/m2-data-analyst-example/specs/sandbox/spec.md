# Spec Delta

## Context

This delta ADDS one requirement to the `sandbox` capability and MODIFIES none. The composed data-analyst path crosses two capabilities — the `PerSession` tool set from `m2-sandbox-governance` and the suspend/resume lifecycle from `m2-sandbox-lifecycle-resume` — and neither capability's requirement block states the assembled ordering, so nothing today makes it observable. The requirement below is the first place that ordering is normative, and it carries the one new composition scenario `sandbox.data-analyst-resume-and-page`.

Ten existing ids are reused unchanged and are not restated here: `sandbox.egress-denied`, `sandbox.secret-injected`, `sandbox.secret-leak-redacted`, `sandbox.per-session-persists`, `sandbox.snapshot-on-suspend`, `sandbox.resume-restores`, `sandbox.replay-no-reexec`, `sandbox.output-to-store` and `sandbox.output-stored-redacted` keep their blocks and owners (`m2-sandbox-adapter-conformance`, `m2-sandbox-governance`, `m2-sandbox-lifecycle-resume` and `protect-tool-output-storage`), and `working-state.output-paging` keeps its block in the `working-state` capability (`openspec/specs/working-state/spec.md:156-159`). `sandbox.output-stored-redacted` and `working-state.output-paging` are consumed by the composed path and are not re-specified here: paging is owned by `std/outputs.NewReadOutput` and output redaction by `protect-tool-output-storage`.

This delta adds no redaction rule, no egress rule, no port method and no secret-handling rule; it fixes only the order in which the already-owned rules apply to one composed path. The `Sandbox` and `Workspace` method sets are unchanged, and the durable-enumeration contract stays the optional `types.SandboxResources` with `Enumerate` and `CloseByRef` reached by type assertion (`core/types/sandbox.go:97-102`).

**Scope of the evidence.** The requirement's scenario asserts harness ordering and public-path reachability. It does not assert network-layer egress denial, real interpreter execution, router `Cascade`, provider failover, shared quota, multi-batch yielding or the `ClassContextOverflow` backstop; those stay with their named owners and are reported as excluded rather than covered.

## ADDED Requirements

### Requirement: Data analyst suspend, resume and page composition

A read-only warehouse query MUST carry a `PerSession` `SandboxPolicy` with an empty `Egress.Allow`, MUST inject its credential only through `Workspace.SetEnv`, and MUST redact output for injected values and personally identifying patterns BEFORE journaling and before an overflow body reaches the `OutputStore`. On suspension the produced workspace ref MUST be stored before the suspension persists, and a later turn on another stack MUST open from that ref and replay a journaled result.

#### Scenario: data analyst resumes and pages
ID: `sandbox.data-analyst-resume-and-page` (proposed)
- WHEN a session runs a read-only warehouse query that writes an extract, then suspends and resumes on a second stack that opens from the stored ref
- THEN the resumed turn reads the extract with no extra `Put` and no re-execution, and `read_output(ref, range)` pages back the requested slice with no personally identifying value
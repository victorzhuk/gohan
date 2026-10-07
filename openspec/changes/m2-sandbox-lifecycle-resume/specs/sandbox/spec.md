# Spec Delta

## Context

This delta MODIFIES one requirement of the `sandbox` capability, `Lifecycle and resume` (`openspec/specs/sandbox/spec.md:122-149`). The requirement's `PerCall` prose and all five of its scenario blocks are reproduced byte-identical; the four scenario ids this change owns (`sandbox.per-session-persists`, `sandbox.snapshot-on-suspend`, `sandbox.resume-restores`, `sandbox.replay-no-reexec`) and the `PerCall` id `sandbox.per-call-closes` keep their exact `ID:` and WHEN/THEN lines. The normative additions land in the requirement prose only: they turn the `PerSession` lifecycle, the snapshot-on-suspend hook, the resume restore and the replay guarantee from Contract rules (rules 4 and 5) into requirement text. No Contract code block changes: the port and value types are unchanged, and the only new type this change declares is an optional session-metadata surface in `core/stores`, outside the capability's Contract block.

## MODIFIED Requirements

### Requirement: Lifecycle and resume

The `PerCall` lifecycle MUST run `Open`, `Exec` and `Close` inside one tool call, and `Close` MUST be called exactly once per call, including when the call panics.

The `PerSession` lifecycle MUST keep one workspace per session across turns: the first call opens it and its `WorkspaceRef` MUST be persisted through the session metadata surface, and a later call MUST `Open` from that ref so files written in an earlier turn are visible without re-upload. On suspension, and at the end of a detached run, the harness MUST call `Workspace.Snapshot` and MUST store the produced `WorkspaceRef` in `Checkpoint.Workspace`. On resume the harness MUST `Open` the workspace from `Checkpoint.Workspace`, so a run resumed on another pod sees the files written before suspension. A completed sandbox call MUST be journaled like any tool result, and `Replay` MUST use the journaled `ExecResult` without calling the adapter's `Exec` again. `Close` for a `PerSession` workspace MUST be called by the reaper when the session's lease expires.

#### Scenario: per-session persistence
ID: `sandbox.per-session-persists`
- WHEN turn 1 writes `data.csv` and turn 2 runs code reading it
- THEN the read succeeds without re-uploading

#### Scenario: snapshot on suspension
ID: `sandbox.snapshot-on-suspend`
- WHEN a run suspends for `HumanApproval` after writing files
- THEN `Checkpoint.Workspace` holds a `WorkspaceRef` produced by `Snapshot`

#### Scenario: resume on another pod
ID: `sandbox.resume-restores`
- WHEN the run is resumed on another pod
- THEN the workspace opened from `Checkpoint.Workspace` contains the files written before suspension

#### Scenario: replay does not re-execute
ID: `sandbox.replay-no-reexec`
- WHEN `Replay` resumes a run that executed code before suspending
- THEN the journaled `ExecResult` is used and the adapter's `Exec` is not called again

#### Scenario: per-call closes
ID: `sandbox.per-call-closes`
- WHEN a `PerCall` tool call finishes or panics
- THEN `Close` has been called exactly once

# Design

## Context

This child implements parent decision 11 and the iterator-validation repair from decision 12.
It follows all behavior repairs because their final adapter interfaces and manifests must satisfy the distribution checks together.
These documents specify required checks, not observed publication availability or current fixture outcomes.

## Decisions

### Declared dependencies, not workspace substitutes

Move every landed adapter's root requirement to existing root `v1.0.0`.
Do not upgrade third-party dependencies.
Verify published availability and checksums.
Use `GOWORK=off` with no manifest replacements for independent adapter compilation, tidy cleanliness and module conformance.
A local replacement cannot satisfy a release verdict.
Keep examples as a development workspace consumer of newly added root options and interfaces.
Verify examples through `task examples:test`.
Do not claim examples compatibility with a released root lacking those APIs.

The PostgreSQL adapter implements token-aware recovery structurally using released parameter/result types.
Do not reference an unpublished root interface in a compile-time adapter assertion.
An adapter release using newly required optional interfaces must follow a published root containing them.
This change has no authority to publish that root or any adapter.

### Verification-only task and CI

Add a verification-only adapter release task using the existing Taskfile conventions.
Name and document its exact command when implementing it.
The draft does not claim the new task already exists.
The task rejects replacements, compiles independent module dependencies, checks tidy cleanliness and executes conformance.
CI executes the same contract.
Neither the task nor CI creates a tag or publication.
Do not substitute workspace conformance for independent dependency compilation.
An unavailable published dependency leaves release verification incomplete.

### Semantic API comparison

Use standard-library type information with module-aware package/export loading.
Keep baseline and candidate dependency graphs separate.
Detect removed generic methods, inferred variable type changes and imported type identity changes.
Accept documented additive keyed-struct growth.
Retain existing v0/v1 reporting behavior.
Reject unrelated package-load failures rather than suppressing them.

Historical adapter `v1.0.0` baselines declare erroneous root `v0.1.0` dependencies.
The approved comparison exception requires verification that matching root `v1.0.0` points to the same release commit.
Permit that single requirement correction only for verified historical adapter tags in a temporary comparison copy.
Print the original requirement, corrected requirement and applied exception.
Leave all other dependencies unchanged.
Do not alter tags, repair original published manifests or hide the original release defect.
This exception never applies to independent release verification and never substitutes a local root.

### Validation repairs and documentation

Handle all fourteen existing unchecked iterator errors without disabling checks or removing assertions.
Preserve independently owned research-report edits.
Coordinate their owner if a required test repair intersects that implementation.
Update existing release, compatibility and testing docs and changelog entries.
Verify every documented command exists after implementation.
Keep new task naming explicit rather than documenting a guessed command as existing.

## Scope and Ordering

Integrate authority, recovery, provider, sandbox and network outcomes before this child.
Preserve all preceding registry, generated index, docs and changelog changes.
The manifest declares broad directory overlap for iterator repairs and adapter manifest/conformance changes.
This child does not acquire ownership of research-report implementation.
Parent integration verifies the complete combined tree and the five original research-report edits.

## Acceptance

Establish failing semantic API assertions before replacing the checker.
Exercise each compatibility mutation and existing v0/v1 reporting tests.
Run `task api:check` after the semantic checker repairs.
Verify each adapter independently with `GOWORK=off`, clean manifests and no replacements.
Use a replacement-positive fixture to prove the new task refuses replacements.
Verify no tag or publication results.
Run both root and examples legs of `task lint` without weakened checks.
Verify workspace examples separately through `task examples:test`.

## Limits

Focused commands have a three-minute process deadline and a two-minute Go test timeout.
Full module/store commands have a twenty-minute process deadline and a ten-minute test timeout.
Use two package workers, two parallel tests and two lint workers.
Preserve configured cache and temporary build paths.
Allow one fixture repair and at most two further diagnostic reruns per boundary.
A flaky retry does not establish acceptance.
Stop for changed contracts, destructive operations, missing credentials, unresolved published dependencies or exhausted repair limits.
Parent integration retains the existing M2 floor and reports unrelated blockers separately.

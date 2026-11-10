## ADDED Requirements

### Requirement: Shipped adapter compatibility uses declared published dependencies

Independent adapter verification MUST disable the workspace with `GOWORK=off`.
It MUST reject manifest replacements and verify published dependency availability, checksums, independent compilation, tidy cleanliness and conformance.
Landed adapters MUST use the published root `v0.2.0` without third-party upgrades.
Examples MUST remain a development workspace consumer and pass their existing acceptance wrapper.
Verification MUST NOT create tags or publications.

#### Scenario: A workspace replacement hides an incompatible adapter dependency
- WHEN workspace compilation succeeds but the declared published dependency cannot compile the adapter
- THEN independent adapter verification fails rather than accepting the workspace result

#### Scenario: A manifest contains a local replacement
- WHEN the verification-only adapter release task receives a manifest replacement
- THEN it refuses a passing release verdict without publishing or tagging anything

### Requirement: Public API compatibility comparison preserves semantic type identity

API comparison MUST use standard-library type information and module-aware dependency/export loading.
Baseline and candidate graphs MUST remain separate.
The comparison MUST detect generic method removal, inferred variable type changes and imported type identity changes.
It MUST accept documented additive keyed-struct growth and retain existing v0/v1 reporting behavior.
Unrelated load errors MUST fail comparison.

#### Scenario: Public generic methods or variable types change incompatibly
- WHEN a candidate removes a generic method or changes an inferred variable type or imported type identity
- THEN the checker reports the semantic incompatibility

#### Scenario: A keyed public struct gains a permitted additive field
- WHEN the candidate adds a field under the documented keyed-struct compatibility rule
- THEN the checker accepts that additive growth

### Requirement: Historical baseline correction remains narrowly bounded

Only the verified published adapter baseline `adapter/otel/v0.1.0` MAY receive the approved root-requirement correction in a temporary copy.
Verification MUST establish that it and root `v0.1.0` share the same release commit.
The correction MUST change only the declared root requirement and its workspace replacement.
All other dependencies MUST remain unchanged.
This exception MUST NOT modify tags, hide the published defect or substitute a local root in independent release verification.

#### Scenario: A verified published adapter baseline declares an unresolvable root requirement
- WHEN the comparison verifies `adapter/otel/v0.1.0` and the matching root `v0.1.0` release commit
- THEN its temporary baseline copy changes only the root requirement `v0.0.0` and its workspace replacement to published root `v0.1.0`, prints the original requirement, corrected requirement and the applied exception, and reports that exception

#### Scenario: Another dependency or unrelated load fails
- WHEN a baseline needs a different correction or has an unrelated load error
- THEN comparison fails without applying the historical exception

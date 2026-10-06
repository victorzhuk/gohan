# Spec Delta

## ADDED Requirements

### Requirement: Shared provider conformance contract

The implementation MUST provide deterministic shared conformance inputs for provider adapters. Each adapter MUST run the shared suite through its public model call path against recorded provider responses. Adapter scenario subtests MUST use the registered scenario ID as the subtest name. The suite MUST check error class mapping, Retry-After preservation, explicit cache breakpoint ordering and limit, constrained-schema refusal before sending, usage estimation, Raw provider fidelity, per-key isolation, and provider-key redaction. A reference double MAY prove that the assertions detect a deliberate non-conforming result. A reference-double result does not establish adapter coverage.

The implementation MUST reject an unsupported output fidelity combination during public Build. The error MUST identify the profile and missing block kind. The implementation MUST reject a static governed-field collision in `ModelOptions.Extra` during Build and MUST return a permanent model error for a dynamic collision before the provider request is sent. Provider adapters MUST NOT ignore `ResponseSchema` when the profile lacks `Caps.Constrained`.

#### Scenario: model extra collision
ID: `model.extra-collision`
- WHEN static `ModelOptions.Extra` contains `max_tokens`
- THEN `Build` fails before any provider call, and a dynamic `max_tokens` collision returns `ClassPermanent` before sending a request

#### Scenario: model undeclared fidelity
ID: `model.undeclared-fidelity`
- WHEN a flow tool can return `types.KindFile` and the selected profile has no `Caps.Fidelity` entry for that kind
- THEN public `Build` fails and the error names the profile and `types.KindFile`

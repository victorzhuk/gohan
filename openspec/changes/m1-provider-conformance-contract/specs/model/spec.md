# Spec Delta

## ADDED Requirements

### Requirement: Shared provider conformance contract

The implementation MUST provide deterministic shared conformance inputs, and each adapter MUST run the shared suite through its public model call path against recorded responses. Build MUST reject an unsupported output fidelity combination and a static governed-field collision, and a dynamic collision MUST return a permanent model error before the request is sent. Adapters MUST NOT ignore `ResponseSchema` without `Caps.Constrained`.

#### Scenario: shared suite coverage
- WHEN the shared suite runs against an adapter's recorded responses
- THEN it checks error class mapping, Retry-After preservation, cache breakpoint order and limit, constrained-schema refusal before sending, usage estimation, Raw provider fidelity, per-key isolation and key redaction, and a reference double that violates an assertion fails the suite

#### Scenario: model extra collision
ID: `model.extra-collision`
- WHEN static `ModelOptions.Extra` contains `max_tokens`
- THEN `Build` fails before any provider call, and a dynamic `max_tokens` collision returns `ClassPermanent` before sending a request

#### Scenario: model undeclared fidelity
ID: `model.undeclared-fidelity`
- WHEN a flow tool can return `types.KindFile` and the selected profile has no `Caps.Fidelity` entry for that kind
- THEN public `Build` fails and the error names the profile and `types.KindFile`

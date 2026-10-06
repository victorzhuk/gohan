# Spec Delta

## MODIFIED Requirements

### Requirement: Taint survives extraction

`std/flow.Quarantine[Out]` MUST carry the origins of the blocks it read back to
its caller, and recording that result in a session MUST leave those origins on
the recorded result blocks, so a later tool call whose argument contains an
extracted substring is tainted by rule 1. An extraction MUST NOT add, drop or
rewrite an origin, and a value MUST NOT become trusted by passing through a
model.

#### Scenario: quarantine output tainted
ID: `taint.quarantine-carries-origin`
- WHEN `Quarantine[Invoice]` extracts `iban` from an untrusted PDF and the model passes it to `pay_supplier`
- THEN the `iban` argument is tainted with the PDF's origin and the default policy forces `Ask`

#### Scenario: sub-flow output tainted
ID: `taint.subflow-carries-origin`
- WHEN a `FlowAsTool` child read untrusted content and returns a value derived from it
- THEN the parent sees that value with the child's source origin, not `OriginTool` of the child alone

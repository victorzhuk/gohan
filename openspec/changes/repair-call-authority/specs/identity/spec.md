## ADDED Requirements

### Requirement: Identity exclusions apply during argument decoding

A schema-excluded identity field MUST also remain excluded during execution argument decoding.
The exclusion MUST follow the same nested-field and JSON-name rules as schema derivation.
Transport principal identity MUST remain authoritative.

#### Scenario: Excluded identity is supplied through the public tool path
ID: `identity.excluded-arguments-not-decoded`
- WHEN a model supplies an excluded identity field to a typed tool
- THEN the callback receives the field's zero value and the verified transport principal remains unchanged

#### Scenario: Identity is supplied inside a nested argument
ID: `identity.nested-excluded-arguments-not-decoded`
- WHEN an excluded identity name appears in a nested field represented by the derived schema
- THEN the callback receives no model-supplied value for that field

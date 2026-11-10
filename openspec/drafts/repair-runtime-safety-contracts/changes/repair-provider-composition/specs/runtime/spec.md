## ADDED Requirements

### Requirement: Foreign runtime construction installs call identity

A shipped foreign runtime MUST carry backend-generated tool call IDs through its public conversation path before the governed call boundary.
A valid call MUST reach permission, accounting and journal processing with that identity.

#### Scenario: Eino executes a governed tool through the shipped constructor
ID: `runtime.eino-governed-call-identity`
- WHEN a registered Eino flow invokes a tool through its foreign conversation
- THEN its backend call ID reaches the gate and journal and the tool executes once when authorized

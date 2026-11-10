## ADDED Requirements

### Requirement: Terminal success follows successful persistence

A runtime-carried stop reason MUST be preserved only after the terminal store transition succeeds. A failed terminal transition MUST emit an error or terminal failure and MUST NOT emit `Done`.

#### Scenario: A limit stop encounters a failed finish write
ID: `runtime.finish-error-no-done`
- WHEN a step carries a limit stop and terminal persistence fails
- THEN no `Done` is delivered and the failure remains visible

### Requirement: Native tool admissions remain cumulative

Native suspension and recovery MUST preserve the cumulative admitted tool count. A pending prepaid call MUST execute without another charge. A subsequent new batch MUST be checked against all earlier admissions in the same run.

#### Scenario: A later batch suspends and resumes
ID: `runtime.resume-tool-budget-cumulative`
- WHEN earlier batches and a pending approved call have consumed the run's tool limit
- THEN resume executes the prepaid call but admits no additional call beyond the limit

### Requirement: Foreign runtime construction installs call identity

A shipped foreign runtime MUST carry each backend-generated tool call ID through its public conversation path before the governed call boundary. A valid call MUST reach permission, accounting and journal processing with that identity.

#### Scenario: Eino executes a governed tool through the shipped constructor
ID: `runtime.eino-governed-call-identity`
- WHEN a registered Eino flow invokes a tool through its foreign conversation
- THEN its backend call ID reaches the gate and journal and the tool executes once when authorized

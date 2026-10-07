# Spec Delta

## MODIFIED Requirements

### Requirement: Projections are views

The harness MUST apply the declared projections in order before it calls the caller assembler. Each projection MUST receive the selected model profile and the current history version. The harness MUST use the projected messages only for assembly. A projection MUST NOT mutate the source history or write to a store. The harness MUST retain the existing blob resolution, redaction, and operator-turn fence after the caller assembler.

`ClearToolResults` MUST identify each result's tool through its matching `ToolUse.ID`. It MUST count the last `Keep` tool uses in history order. It MUST preserve all `ToolUse` blocks and the position of each `ToolResult`. It MUST preserve results within the retained window. It MUST preserve results for tools in `Exclude`, including `notes_read` and `notes_write` regardless of caller configuration.

`ClearToolResults` MUST replace eligible result content with a deterministic placeholder. It MUST preserve the result identity, outcome, error, and origin. It MUST decide `ClearAtLeast` through `ContextBudget` with a deterministic estimator. Cleared tokens MUST mean the positive difference between the original estimate and the candidate estimate, including placeholder cost. A firing MUST clear at least `ClearAtLeast` tokens or leave the history unchanged.

#### Scenario: session log untouched
ID: `context.projection-does-not-write`
- WHEN `ClearToolResults` clears three old results during assembly
- THEN `SessionLog.Load` still returns the full results and `History.Version` is unchanged

#### Scenario: deterministic projection
ID: `context.projection-deterministic`
- WHEN the same `History.Version` is assembled twice for the same profile
- THEN the projected request bytes are identical

#### Scenario: keep last N
ID: `context.keep-last-n`
- WHEN `ClearToolResults{Keep: 3}` runs over a history with six completed tool calls
- THEN the three most recent results are intact, the three older ones are placeholders, and all six `ToolUse` blocks are present

#### Scenario: notes never cleared
ID: `context.notes-excluded`
- WHEN `notes_read` results are older than the last `Keep` tool uses
- THEN they are not cleared

#### Scenario: batched clearing
ID: `context.clear-at-least`
- WHEN clearing is triggered with `ClearAtLeast: 15000`
- THEN at least 15 000 tokens are cleared in one firing or none at all

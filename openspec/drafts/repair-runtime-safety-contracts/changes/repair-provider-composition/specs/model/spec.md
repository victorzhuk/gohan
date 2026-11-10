## ADDED Requirements

### Requirement: Provider tool streams complete each call independently

Argument previews MUST carry their call identity.
Each completed tool block MUST produce one executable `ToolUse` with complete arguments.
Indexed calls MUST NOT share argument accumulation.
Forced structured-output mapping MUST retain its existing text behavior.
Unfinished arguments MUST remain non-executable.

#### Scenario: Anthropic returns multiple tool blocks
ID: `model.anthropic-complete-tool-blocks`
- WHEN an Anthropic response streams two indexed tool blocks with separate argument fragments
- THEN both complete calls retain their own ID, name and arguments and execute through the native conversation as authorized

#### Scenario: Provider arguments stream before completion
ID: `model.provider-argument-preview-identity`
- WHEN either provider streams tool argument fragments
- THEN previews carry the corresponding call identity and exactly one complete block is admitted per completed call

#### Scenario: Forced output and unfinished arguments
ID: `model.forced-output-and-tool-truncation`
- WHEN a response uses forced structured output or ends before tool arguments complete
- THEN forced output retains its text mapping and incomplete arguments are not executed

### Requirement: OpenAI stream termination is final

An OpenAI iterator MUST yield nothing after its consumer returns false or after a terminal stream error.
Estimated usage MUST be emitted only on clean completion when reported usage is absent.
The response body MUST be released on every exit.

#### Scenario: The consumer stops before usage arrives
ID: `model.openai-consumer-stop-final`
- WHEN a consumer breaks after a text or reasoning chunk
- THEN iteration returns without a later usage yield or panic and the response body closes

#### Scenario: A stream decode fails before usage arrives
ID: `model.openai-stream-error-final`
- WHEN a response contains a terminal decode or read failure
- THEN the iterator yields the failure once and produces no later usage chunk

### Requirement: Optional cache usage details remain optional

A reported usage record without cache details MUST remain valid for `CacheAuto`.
Missing details MUST produce zero cache-token counts without a panic.

#### Scenario: Automatic cache mode receives only total usage
ID: `model.openai-cache-details-absent`
- WHEN `CacheAuto` receives nonzero usage totals without prompt-token details
- THEN usage is reported with zero cached tokens and iteration completes safely

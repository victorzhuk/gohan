# 0142. Declare what the first rows implemented: the tool-argument validator and the identity-field matcher

Status: accepted

## Context

The row-5 drift gate compares what the specs declare against what the Go declares. Rows 1–4 implemented 82 of the 411 normative identifiers with no kind or shape mismatch, but five names the Go exports appear in no spec block: `ValidateToolArgs`, `ToolArgsError` and `ArgsErrorResult` from `core/types/tool_args.go`, and `IdentityFieldMatcher` and `NewIdentityFieldMatcher` from `core/identity_args.go`.

Both families arrived the same way: the plan named an API the spec had left as prose. Chunk `2.3` says "validate complete `ToolUse.Args`; reject duplicate JSON keys as `ToolResult` with `Failed`/`Permanent`", and chunk `4.4` says "define the identity exclusion contract consumed by row 6". A name that only the plan carries is a contract nobody can hold to, and `spec:types` cannot see it.

## Decision

1. `messages` declares the validator in its own contract block, beside `ToolUse`/`ToolResult`/`ToolError`: `ToolArgsError{Reason, Message}`, `ValidateToolArgs(args json.RawMessage) error` and `ArgsErrorResult(err error) ToolResult`. A prose rule fixes the reasons (`duplicate_key`, `invalid_utf8`, `syntax`) and the result shape (`Failed`/`Permanent`).
2. `identity` declares the mechanism rule 7 already describes: the configurable matcher type and its constructor, taking the patterns as an argument. The default field list (`user_id`, `tenant`, `customer_id`, …) stays a `std` policy value, because the core budget keeps every default out of `core`.
3. Nothing moves in the implementation. The Go shapes are what the specs now say; the pass only supplies the words the specs were missing.

## Consequences

The drift gate reports no implemented-but-undeclared identifier, and `spec:types` sees both families, so a future shape change fails the index instead of drifting quietly. The next row that needs either API (`6.5`/`6.6` for the schema walker and the call path, `21.x` for `Build`'s warning) consumes it by name rather than re-deriving it.

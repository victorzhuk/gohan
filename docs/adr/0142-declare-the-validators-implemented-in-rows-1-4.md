# 0142. Declare what the first rows implemented: the tool-argument validator and the identity-field matcher

Status: accepted

## Context

The row-5 drift gate compares what the specs declare against what the Go declares. Rows 1–4 implemented 82 of the 411 normative identifiers with no kind or shape mismatch, but five names the Go exports appear in no spec block: `ValidateToolArgs`, `ToolArgsError` and `ArgsErrorResult` from `core/types/tool_args.go`, and `IdentityFieldMatcher` and `NewIdentityFieldMatcher` from `core/identity_args.go`.

Both families arrived the same way: the plan named an API the spec had left as prose. Chunk `2.3` says "validate complete `ToolUse.Args`; reject duplicate JSON keys as `ToolResult` with `Failed`/`Permanent`", and chunk `4.4` says "define the identity exclusion contract consumed by row 6". A name that only the plan carries is a contract nobody can hold to, and `spec:types` cannot see it.

## Decision

1. `messages` declares the validator in its own contract block, beside `ToolUse`/`ToolResult`/`ToolError`: `ToolArgsError{Reason, Message}`, `ValidateToolArgs(args json.RawMessage) error` and `ArgsErrorResult(err error) ToolResult`. A prose rule fixes the reasons (`duplicate_key`, `invalid_utf8`, `syntax`) and the result shape (`Failed`/`Permanent`).
2. `identity` declares the mechanism rule 7 already describes: the configurable matcher type and its constructor, taking the patterns as an argument. The default field list (`user_id`, `tenant`, `customer_id`, …) stays a `std` policy value, because the core budget keeps every default out of `core`.
3. Nothing moves in the implementation. The Go shapes are what the specs now say; the pass only supplies the words the specs were missing.

## Third pass, after row 11

`EventLog.Read` returns `Event` values and `Done` is the run's terminal event, but the `Done` declaration carried no `isEvent()`, so the terminal record could not be logged as a payload and chunk 11.3 had to carry it in a store-local envelope. The `streams` contract now declares `func (Done) isEvent()`, which is what the surrounding prose already said.

## Second pass, after row 6

The drift audit of rows 1-4 left one more of the same class: `tools/spec.md:134` uses `gohan.Retryable(err)` in its error-mapping rule and its scenario `tools.classified-error` asserts the marker's effect, but no Go block declared it. The tool contract now declares `func Retryable(err error) error` beside the tool sentinels, so the index sees the function the driver exports.

Declaring it exposed a limit in the index's collision check, which maps a declaration to a package through its capability alone: `Retryable` is also an `ErrorKind` member in package `types`, so the check reported a collision that does not exist, because the function is driver-declared (ADR-0139: the driver holds what users call directly). The check now knows the driver-declared names from ADR-0139's list and routes them to package `gohan`.

`ToolArgsError` joined the named waivers in the catalogue requirement: it becomes the same model-visible `Failed`/`Permanent` result as `ToolError`, so it never reaches a transport.

## Consequences

The drift gate reports no implemented-but-undeclared identifier, and `spec:types` sees both families, so a future shape change fails the index instead of drifting quietly. The next row that needs either API (`6.5`/`6.6` for the schema walker and the call path, `21.x` for `Build`'s warning) consumes it by name rather than re-deriving it.

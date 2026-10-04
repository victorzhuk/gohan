package types

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrorCode values are the closed catalog in messages/spec.md
// (Requirement: Error catalog). Every sentinel and typed error the specs
// declare maps to exactly one row, except *SuspendError and ToolError,
// which never reach a transport.

type ErrorCode string

const (
	CodeNoPrincipal         ErrorCode = "gohan.no_principal"
	CodeSessionForbidden    ErrorCode = "gohan.session_forbidden"
	CodeApproverNotEligible ErrorCode = "gohan.approver_not_eligible"
	CodeRunActive           ErrorCode = "gohan.run_active"
	CodeRunNotActive        ErrorCode = "gohan.run_not_active"
	CodeSessionHeld         ErrorCode = "gohan.session_held"
	CodeMailboxFull         ErrorCode = "gohan.mailbox_full"
	CodeResumeInsideRun     ErrorCode = "gohan.resume_inside_run"
	CodeOperationExists     ErrorCode = "gohan.operation_exists"
	CodeTokenConsumed       ErrorCode = "gohan.token_consumed"
	CodeTokenExpired        ErrorCode = "gohan.token_expired"
	CodeTokenMismatch       ErrorCode = "gohan.token_mismatch"
	CodeInputInvalid        ErrorCode = "gohan.input_invalid"
	CodeEmptyHistory        ErrorCode = "gohan.empty_history"
	CodeSessionHandedOff    ErrorCode = "gohan.session_handed_off"
	CodeBlobTooLarge        ErrorCode = "gohan.blob_too_large"
	CodeLimitExceeded       ErrorCode = "gohan.limit_exceeded"
	CodeShuttingDown        ErrorCode = "gohan.shutting_down"
	CodeCheckpointIncompat  ErrorCode = "gohan.checkpoint_incompatible"
	CodeToolDenied          ErrorCode = "gohan.tool_denied"
	CodeGuardBlocked        ErrorCode = "gohan.guard_blocked"
	CodeProviderKeyMissing  ErrorCode = "gohan.provider_key_missing"
	CodeProviderKeyRejected ErrorCode = "gohan.provider_key_rejected"
	CodeModelRateLimited    ErrorCode = "gohan.model_rate_limited"
	CodeModelUnavailable    ErrorCode = "gohan.model_unavailable"
	CodeModelVersionDrift   ErrorCode = "gohan.model_version_drift"
	CodeContextOverflow     ErrorCode = "gohan.context_overflow"
	CodeContentPolicy       ErrorCode = "gohan.content_policy"
	CodeAborted             ErrorCode = "gohan.aborted"
	CodeStreamInterrupted   ErrorCode = "gohan.stream_interrupted"
	CodeConfiguration       ErrorCode = "gohan.configuration"
	CodeVersionConflict     ErrorCode = "gohan.version_conflict"
	CodeStructuredOutput    ErrorCode = "gohan.structured_output"
	CodeChainStep           ErrorCode = "gohan.chain_step"
	CodeSubflowPartial      ErrorCode = "gohan.subflow_partial"
	CodeOutcomeUnknown      ErrorCode = "gohan.outcome_unknown"
	CodeInternal            ErrorCode = "gohan.internal"
)

// Problem is the client-facing shape of any Go error. Detail is rendered
// from a fixed template per code with only the allow-listed fields; the
// caller fills Instance with the run id.
type Problem struct {
	Code       ErrorCode
	Kind       ErrorKind
	Status     int
	RetryAfter time.Duration
	Title      string
	Detail     string
	Instance   string
	Fields     map[string]string
}

type catalogRow struct {
	code   ErrorCode
	kind   ErrorKind
	status int
	retry  time.Duration
	title  string
	detail func(map[string]string) string
}

func fixedDetail(s string) func(map[string]string) string {
	return func(map[string]string) string { return s }
}

// fielded renders withField when the field is present, else the bare text.
func fielded(bare, withField, field string) func(map[string]string) string {
	return func(f map[string]string) string {
		if v := f[field]; v != "" {
			return fmt.Sprintf(withField, v)
		}
		return bare
	}
}

var catalog = []catalogRow{
	{CodeNoPrincipal, Permanent, 401, 0, "No principal", fixedDetail("no principal in context")},
	{CodeSessionForbidden, Permanent, 403, 0, "Session forbidden", fixedDetail("principal may not access this session")},
	{CodeApproverNotEligible, Permanent, 403, 0, "Approver not eligible", fixedDetail("principal may not approve this request")},
	{CodeRunActive, Retryable, 409, 0, "Run active", fixedDetail("a run lease is still active on this session")},
	{CodeRunNotActive, Permanent, 409, 0, "Run not active", fixedDetail("session has no active run")},
	{CodeSessionHeld, Permanent, 409, 0, "Session held", fixedDetail("session is under legal hold")},
	{CodeMailboxFull, Retryable, 429, time.Second, "Mailbox full", fixedDetail("run mailbox is full")},
	{CodeResumeInsideRun, Permanent, 409, 0, "Resume inside run", fixedDetail("resume called from inside a run")},
	{CodeOperationExists, Permanent, 409, 0, "Operation exists", fielded("operation id already recorded", "operation %s already recorded", "run_id")},
	{CodeTokenConsumed, Permanent, 410, 0, "Token consumed", fixedDetail("resume token already consumed")},
	{CodeTokenExpired, Permanent, 410, 0, "Token expired", fixedDetail("resume token expired")},
	{CodeTokenMismatch, Permanent, 422, 0, "Token mismatch", fixedDetail("resume token belongs to another flow or runtime")},
	{CodeInputInvalid, Permanent, 422, 0, "Input invalid", fixedDetail("delivered input does not match the requested schema")},
	{CodeEmptyHistory, Permanent, 422, 0, "Empty history", fixedDetail("session has no messages")},
	{CodeSessionHandedOff, Permanent, 409, 0, "Session handed off", fixedDetail("session is under human control")},
	{CodeBlobTooLarge, Permanent, 413, 0, "Blob too large", fixedDetail("block exceeds the profile's blob limit")},
	{CodeLimitExceeded, Permanent, 422, 0, "Limit exceeded", fielded("run limit exceeded", "%s limit exceeded", "limit")},
	{CodeShuttingDown, Retryable, 503, time.Second, "Shutting down", fixedDetail("stack is shutting down")},
	{CodeCheckpointIncompat, Permanent, 409, 0, "Checkpoint incompatible", fixedDetail("checkpoint schema or backend version incompatible")},
	{CodeToolDenied, Permanent, 403, 0, "Tool denied", fielded("tool call denied by policy", "tool %s denied by policy", "tool")},
	{CodeGuardBlocked, Permanent, 422, 0, "Guard blocked", fielded("guard blocked the run", "guard blocked the %s stage", "stage")},
	{CodeProviderKeyMissing, Permanent, 422, 0, "Provider key missing", fixedDetail("no provider key for this tenant and profile")},
	{CodeProviderKeyRejected, Permanent, 422, 0, "Provider key rejected", fielded("provider key rejected", "provider key %s rejected", "key_id")},
	{CodeModelRateLimited, Retryable, 429, 0, "Model rate limited", fielded("provider rate limited the request", "provider rate limited the request; retry after %s", "retry_after")},
	{CodeModelUnavailable, Retryable, 503, 0, "Model unavailable", fixedDetail("provider is temporarily unavailable")},
	{CodeModelVersionDrift, Permanent, 409, 0, "Model version drift", fixedDetail("model version drifted from the recorded manifest")},
	{CodeContextOverflow, Permanent, 422, 0, "Context overflow", fixedDetail("context exceeds the model's window")},
	{CodeContentPolicy, Permanent, 422, 0, "Content policy", fixedDetail("provider refused the content")},
	{CodeAborted, Permanent, 422, 0, "Aborted", fielded("run aborted", "run aborted: %s", "reason")},
	{CodeStreamInterrupted, Retryable, 0, 0, "Stream interrupted", fixedDetail("stream closed without completing")},
	{CodeConfiguration, Permanent, 500, 0, "Configuration error", fixedDetail("invalid or missing configuration")},
	{CodeVersionConflict, Permanent, 409, 0, "Version conflict", fixedDetail("append with a stale version")},
	{CodeStructuredOutput, Permanent, 422, 0, "Structured output", fixedDetail("model output failed schema validation")},
	{CodeChainStep, Permanent, 500, 0, "Chain step failed", fielded("chain step failed", "chain step %s failed", "stage")},
	{CodeSubflowPartial, Permanent, 422, 0, "Partial subflow results", fixedDetail("subflow finished with partial results")},
	{CodeOutcomeUnknown, Retryable, 409, 0, "Outcome unknown", fixedDetail("side effect may or may not have happened; verify before retrying")},
	{CodeInternal, Permanent, 500, 0, "Internal error", fixedDetail("internal error")},
}

var catalogByCode = func() map[ErrorCode]catalogRow {
	m := make(map[ErrorCode]catalogRow, len(catalog))
	for _, r := range catalog {
		m[r.code] = r
	}
	return m
}()

type sentinelRow struct {
	err error
	row catalogRow
}

var sentinelRows = []sentinelRow{
	{ErrNoPrincipal, catalog[0]},
	{ErrSessionForbidden, catalog[1]},
	{ErrApproverNotEligible, catalog[2]},
	{ErrRunActive, catalog[3]},
	{ErrRunNotActive, catalog[4]},
	{ErrSessionHeld, catalog[5]},
	{ErrMailboxFull, catalog[6]},
	{ErrResumeInsideRun, catalog[7]},
	{ErrOperationExists, catalog[8]},
	{ErrTokenConsumed, catalog[9]},
	{ErrTokenExpired, catalog[10]},
	{ErrTokenMismatch, catalog[11]},
	{ErrInputInvalid, catalog[12]},
	{ErrNotSuspendable, catalog[12]},
	{ErrEmptyHistory, catalog[13]},
	{ErrSessionHandedOff, catalog[14]},
	{ErrBlobTooLarge, catalog[15]},
	{ErrShuttingDown, catalog[17]},
	{ErrCheckpointIncompatible, catalog[18]},
	{ErrNoProviderKey, catalog[21]},
	{ErrVersionConflict, catalog[31]},
	{ErrStructuredOutput, catalog[32]},
	{ErrToolName, catalog[30]},
	{ErrToolDescription, catalog[30]},
	{ErrManifestDrift{}, catalog[30]},
	{ErrMemoryStoreRequired, catalog[30]},
	{ErrNoTokenEstimator, catalog[30]},
	{ErrSessionIndexRequired, catalog[30]},
	{ErrSchemaTooOld, catalog[30]},
	{ErrPartitionMissing, catalog[30]},
	{ErrShutdownIncomplete, catalog[30]},
	{ErrEgressPolicyRequired, catalog[30]},
}

func stageName(s GuardStage) string {
	switch s {
	case StageInput:
		return "input"
	case StageToolResult:
		return "tool_result"
	case StageContext:
		return "context"
	case StageOutput:
		return "output"
	case StageProvider:
		return "provider"
	}
	return fmt.Sprintf("stage(%d)", int(s))
}

func fieldsFor(row catalogRow, err error) map[string]string {
	switch row.code {
	case CodeAborted:
		var e *AbortError
		if errors.As(err, &e) {
			return map[string]string{"reason": e.Reason}
		}
	case CodeGuardBlocked:
		var e *GuardBlockedError
		if errors.As(err, &e) {
			return map[string]string{"stage": stageName(e.Stage)}
		}
	case CodeLimitExceeded:
		var e *LimitExceededError
		if errors.As(err, &e) {
			return map[string]string{"limit": e.Limit}
		}
	}
	return nil
}

func modelRow(e *ModelError) (catalogRow, map[string]string) {
	switch e.Class {
	case ClassRateLimited:
		f := map[string]string{"retry_after": e.RetryAfter.String()}
		return catalog[23], f
	case ClassTransient:
		return catalog[24], nil
	case ClassVersionDrift, ClassDeprecated:
		return catalog[25], nil
	case ClassContextOverflow:
		return catalog[26], nil
	case ClassContentPolicy:
		return catalog[27], nil
	case ClassAuth:
		return catalog[22], nil
	}
	return catalog[36], nil
}

func buildProblem(row catalogRow, fields map[string]string) Problem {
	p := Problem{
		Code:       row.code,
		Kind:       row.kind,
		Status:     row.status,
		RetryAfter: row.retry,
		Title:      row.title,
		Detail:     row.detail(fields),
		Fields:     fields,
	}
	return p
}

// ProblemOf converts err to the client-facing Problem. It matches the
// innermost known error first, so a *StepError wrapping a *ModelError
// reports the model code. Unknown errors become gohan.internal; the
// transport logs the cause with the run id.
func ProblemOf(err error) Problem {
	internal := buildProblem(catalog[36], nil)
	if err == nil {
		return internal
	}
	var step *StepError
	for {
		var se *StepError
		if errors.As(err, &se) && se.Err != nil {
			step = se
			err = se.Err
			continue
		}
		break
	}
	for _, sr := range sentinelRows {
		if errors.Is(err, sr.err) {
			return buildProblem(sr.row, fieldsFor(sr.row, err))
		}
	}
	var merr *ModelError
	if errors.As(err, &merr) {
		row, fields := modelRow(merr)
		return buildProblem(row, fields)
	}
	var abort *AbortError
	if errors.As(err, &abort) {
		return buildProblem(catalog[28], map[string]string{"reason": abort.Reason})
	}
	var guard *GuardBlockedError
	if errors.As(err, &guard) {
		return buildProblem(catalog[20], map[string]string{"stage": stageName(guard.Stage)})
	}
	var limit *LimitExceededError
	if errors.As(err, &limit) {
		return buildProblem(catalog[16], map[string]string{"limit": limit.Limit})
	}
	var partial *PartialError
	if errors.As(err, &partial) {
		return buildProblem(catalog[34], nil)
	}
	// ErrToolCollision and ErrToolSetDrift carry slices, so errors.Is
	// cannot compare them; match by type.
	var coll ErrToolCollision
	if errors.As(err, &coll) {
		return buildProblem(catalog[30], nil)
	}
	var drift ErrToolSetDrift
	if errors.As(err, &drift) {
		return buildProblem(catalog[30], nil)
	}
	var uncertain *UncertainOutcomeError
	if errors.As(err, &uncertain) {
		return buildProblem(catalog[35], nil)
	}
	if step != nil {
		return buildProblem(catalog[33], map[string]string{"stage": step.Step})
	}
	var se *StepError
	if errors.As(err, &se) {
		return buildProblem(catalog[33], map[string]string{"stage": se.Step})
	}
	return internal
}

// sentinelRows indexes into catalog by position; this assertion keeps the
// two tables in sync when a row is inserted.
var _ = func() bool {
	want := []ErrorCode{
		CodeNoPrincipal, CodeSessionForbidden, CodeApproverNotEligible, CodeRunActive,
		CodeRunNotActive, CodeSessionHeld, CodeMailboxFull, CodeResumeInsideRun,
		CodeOperationExists, CodeTokenConsumed, CodeTokenExpired, CodeTokenMismatch,
		CodeInputInvalid, CodeEmptyHistory, CodeSessionHandedOff, CodeBlobTooLarge,
		CodeLimitExceeded, CodeShuttingDown, CodeCheckpointIncompat, CodeToolDenied,
		CodeGuardBlocked, CodeProviderKeyMissing, CodeProviderKeyRejected, CodeModelRateLimited,
		CodeModelUnavailable, CodeModelVersionDrift, CodeContextOverflow, CodeContentPolicy,
		CodeAborted, CodeStreamInterrupted, CodeConfiguration, CodeVersionConflict,
		CodeStructuredOutput, CodeChainStep, CodeSubflowPartial, CodeOutcomeUnknown,
		CodeInternal,
	}
	if len(want) != len(catalog) {
		panic("error catalog: row count mismatch")
	}
	for i, c := range want {
		if catalog[i].code != c {
			panic("error catalog: row " + strings.TrimSpace(string(c)) + " out of order")
		}
	}
	return true
}()

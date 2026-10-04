package types

import (
	"errors"
	"fmt"
	"time"
)

// Sentinel errors, one per capability spec. Messages are the spec's exact
// strings; the error catalog in messages/spec.md maps each to a Problem code.

var (
	// flow
	ErrNoPrincipal      = errors.New("gohan: no principal in context")
	ErrEmptyHistory     = errors.New("gohan: session has no messages")
	ErrSessionHandedOff = errors.New("gohan: session is under human control")

	// identity
	ErrSessionForbidden = errors.New("gohan: principal may not access this session")
	ErrResumeInsideRun  = errors.New("gohan: resume called from inside a run")

	// permission
	ErrApproverNotEligible = errors.New("gohan: principal may not approve this request")

	// runtime
	ErrShuttingDown       = errors.New("gohan: stack is shutting down")
	ErrShutdownIncomplete = errors.New("gohan: shutdown deadline passed with runs in flight")

	// stores
	ErrSessionIndexRequired   = errors.New("gohan: session listing needs a SessionIndex")
	ErrSchemaTooOld           = errors.New("gohan: postgres schema older than this release supports")
	ErrPartitionMissing       = errors.New("gohan: no partition for this row's time")
	ErrRunActive              = errors.New("gohan: session has an active run lease")
	ErrRunNotActive           = errors.New("gohan: session has no active run")
	ErrSessionHeld            = errors.New("gohan: session is under legal hold")
	ErrMailboxFull            = errors.New("gohan: run mailbox is full")
	ErrSignalsPending         = errors.New("gohan: steer signals arrived after the last drain")
	ErrOperationExists        = errors.New("gohan: operation id already recorded")
	ErrCheckpointIncompatible = errors.New("gohan: checkpoint schema or backend version incompatible")
	ErrVersionConflict        = errors.New("gohan: append with a stale version")

	// structured-output
	ErrStructuredOutput = errors.New("gohan: model output failed schema validation")

	// suspension
	ErrTokenConsumed  = errors.New("gohan: resume token already consumed")
	ErrTokenExpired   = errors.New("gohan: resume token expired")
	ErrTokenMismatch  = errors.New("gohan: resume token belongs to another flow or runtime")
	ErrInputInvalid   = errors.New("gohan: delivered input does not match the requested schema")
	ErrNotSuspendable = errors.New("gohan: this flow cannot suspend or resume")

	// tools
	ErrEgressPolicyRequired = errors.New("gohan: tool reaches the network without an EgressPolicy")
	ErrToolName             = errors.New("gohan: tool name must match ^[a-z][a-z0-9_]{0,63}$")
	ErrToolDescription      = errors.New("gohan: tool description carries an instruction directive")

	// model
	ErrNoProviderKey    = errors.New("gohan: no provider key for this tenant and profile")
	ErrNoTokenEstimator = errors.New("gohan: no token estimator configured")

	// working-state
	ErrMemoryStoreRequired = errors.New("gohan: subject memory needs a MemoryStore")
)

type ResumeToken string

type SuspendReason string

const (
	HumanApproval    SuspendReason = "human_approval"
	AwaitingExternal SuspendReason = "awaiting_external"
	AwaitingBatch    SuspendReason = "awaiting_batch"
	AwaitingTool     SuspendReason = "awaiting_tool"
	AwaitingControl  SuspendReason = "awaiting_control"
	AwaitingInput    SuspendReason = "awaiting_input"
	Scheduled        SuspendReason = "scheduled"
	Preempted        SuspendReason = "preempted"
	HumanHandoff     SuspendReason = "human_handoff"
)

// SuspendError never crosses a transport: it is the harness-internal
// suspension signal (catalog waiver).
type SuspendError struct {
	Token   ResumeToken
	Reason  SuspendReason
	Payload any
	WakeAt  time.Time
}

func (e *SuspendError) Error() string {
	return "gohan: run suspended: " + string(e.Reason)
}

type GuardStage int

const (
	StageInput GuardStage = iota
	StageToolResult
	StageContext
	StageOutput
	StageProvider
)

type GuardBlockedError struct {
	Stage    GuardStage
	Reason   string
	Fallback Message
}

func (e *GuardBlockedError) Error() string {
	return "gohan: guard blocked " + stageName(e.Stage) + ": " + e.Reason
}

type AbortError struct {
	Reason string
}

func (e *AbortError) Error() string { return "gohan: run aborted: " + e.Reason }

type LimitExceededError struct {
	Limit string
	Value float64
}

func (e *LimitExceededError) Error() string {
	return fmt.Sprintf("gohan: limit %s exceeded (%g)", e.Limit, e.Value)
}

type UncertainOutcomeError struct {
	Out       any
	Uncertain []CallKey
}

func (e *UncertainOutcomeError) Error() string {
	return "gohan: call outcome uncertain; verify before retrying"
}

type PartialError struct {
	Results []any
	Errors  []error
}

func (e *PartialError) Error() string {
	return fmt.Sprintf("gohan: %d of %d subflow branches failed", len(e.Errors), len(e.Errors)+len(e.Results))
}

type StepError struct {
	Step string
	Err  error
}

func (e *StepError) Error() string {
	if e.Err != nil {
		return "gohan: step " + e.Step + ": " + e.Err.Error()
	}
	return "gohan: step " + e.Step + " failed"
}

func (e *StepError) Unwrap() error { return e.Err }

type ErrorClass int

const (
	ClassRateLimited ErrorClass = iota + 1
	ClassTransient
	ClassContextOverflow
	ClassContentPolicy
	ClassDeprecated
	ClassAuth
	ClassPermanent
	ClassVersionDrift
)

type ModelError struct {
	Class      ErrorClass
	Provider   string
	Status     int
	Code       string
	RetryAfter time.Duration
	Err        error
}

func (e *ModelError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("gohan: model error from %s: %v", e.Provider, e.Err)
	}
	return fmt.Sprintf("gohan: model error from %s (class %d, status %d)", e.Provider, e.Class, e.Status)
}

func (e *ModelError) Unwrap() error { return e.Err }

func (e *ModelError) Retryable() bool {
	return e.Class == ClassRateLimited || e.Class == ClassTransient
}

type ErrManifestDrift struct{ Tool string }

func (e ErrManifestDrift) Error() string {
	return "gohan: tool manifest drift for " + e.Tool
}

type ErrToolCollision struct {
	Name    string
	Sources []string
}

func (e ErrToolCollision) Error() string {
	return "gohan: tool name collision for " + e.Name
}

type ErrToolSetDrift struct {
	Missing, Extra []string
}

func (e ErrToolSetDrift) Error() string {
	return "gohan: tool set drifted from the recorded manifest"
}

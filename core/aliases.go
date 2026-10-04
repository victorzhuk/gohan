package gohan

import (
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

type (
	Role           = types.Role
	Message        = types.Message
	OriginKind     = types.OriginKind
	Origin         = types.Origin
	Block          = types.Block
	BlockKind      = types.BlockKind
	BlockBase      = types.BlockBase
	Text           = types.Text
	Reasoning      = types.Reasoning
	Blob           = types.Blob
	Image          = types.Image
	Audio          = types.Audio
	File           = types.File
	Document       = types.Document
	ToolUse        = types.ToolUse
	ToolResult     = types.ToolResult
	CacheBreak     = types.CacheBreak
	Raw            = types.Raw
	Compaction     = types.Compaction
	CompactionKind = types.CompactionKind
	Outcome        = types.Outcome
	ErrorKind      = types.ErrorKind
	ToolError      = types.ToolError
	DeltaKind      = types.DeltaKind
	FinishReason   = types.FinishReason
	ModelChunk     = types.ModelChunk
	ToolArgsError  = types.ToolArgsError
	Usage          = types.Usage

	CostTags         = types.CostTags
	Credential       = types.Credential
	CredentialSource = types.CredentialSource
	LatencyClass     = types.LatencyClass
	Principal        = types.Principal
	RunInfo          = types.RunInfo
	RunMode          = types.RunMode
	SessionOwner     = types.SessionOwner

	AbortError            = types.AbortError
	AssistantMessage      = types.AssistantMessage
	CallKey               = types.CallKey
	Compacted             = types.Compacted
	Done                  = types.Done
	ErrorClass            = types.ErrorClass
	ErrorCode             = types.ErrorCode
	Event                 = types.Event
	EventMeta             = types.EventMeta
	FeedbackRecorded      = types.FeedbackRecorded
	FeedbackSource        = types.FeedbackSource
	FeedbackTarget        = types.FeedbackTarget
	GuardBlocked          = types.GuardBlocked
	GuardBlockedError     = types.GuardBlockedError
	GuardStage            = types.GuardStage
	LimitExceededError    = types.LimitExceededError
	LimitWarning          = types.LimitWarning
	ModelError            = types.ModelError
	NoticeKind            = types.NoticeKind
	Notifier              = types.Notifier
	PartialError          = types.PartialError
	PatchOp               = types.PatchOp
	Problem               = types.Problem
	ReasoningDelta        = types.ReasoningDelta
	ResumeToken           = types.ResumeToken
	ResultDelta           = types.ResultDelta
	RunNotice             = types.RunNotice
	StateChanged          = types.StateChanged
	StepError             = types.StepError
	SteerApplied          = types.SteerApplied
	StopReason            = types.StopReason
	SuspendError          = types.SuspendError
	SuspendReason         = types.SuspendReason
	Suspended             = types.Suspended
	TextDelta             = types.TextDelta
	ToolArgsDelta         = types.ToolArgsDelta
	ToolFinished          = types.ToolFinished
	ToolStarted           = types.ToolStarted
	UncertainOutcomeError = types.UncertainOutcomeError

	Capabilities          = types.Capabilities
	Effect                = types.Effect
	EgressDenied          = types.EgressDenied
	EgressPolicy          = types.EgressPolicy
	Executor              = types.Executor
	IdentityFieldExcluder = types.IdentityFieldExcluder
	PrivateRanges         = types.PrivateRanges
	RiskTier              = types.RiskTier
	SchemaBuilder         = types.SchemaBuilder
	SchemaOption          = types.SchemaOption
	Tool                  = types.Tool
	ToolSpec              = types.ToolSpec
	Trust                 = types.Trust

	Decision[D any]   = types.Decision[D]
	Decider[S, D any] = types.Decider[S, D]

	SessionLog  = stores.SessionLog
	Checkpoints = stores.Checkpoints
)

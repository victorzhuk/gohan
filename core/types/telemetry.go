package types

import "context"

// Telemetry is the emission port core records spans and metrics through.
// Core declares the slot only: an implementation lives in an adapter, and
// core imports neither std nor an adapter, so no OpenTelemetry type leaks
// into the floor. A nil Telemetry is a no-op at every call site.
type Telemetry interface {
	// StartSpan opens a span and returns the ctx it travels in plus the
	// function that ends it; ending more than once is the adapter's
	// concern, core calls it exactly once.
	StartSpan(ctx context.Context, name string, attrs ...Attr) (context.Context, func(attrs ...Attr))
	// Count adds n to a counter metric.
	Count(ctx context.Context, name string, n int64, attrs ...Attr)
	// Record observes one value on a histogram or gauge metric.
	Record(ctx context.Context, name string, v float64, attrs ...Attr)
}

// Attr is one attribute of a span or metric. Values are limited to the
// kinds the constructors produce, so an adapter can map them without a
// reflection pass.
type Attr struct {
	Key   string
	Value any
}

// String returns a string-valued Attr.
func String(key, v string) Attr { return Attr{Key: key, Value: v} }

// Int returns an integer-valued Attr.
func Int(key string, v int64) Attr { return Attr{Key: key, Value: v} }

// Float returns a float-valued Attr.
func Float(key string, v float64) Attr { return Attr{Key: key, Value: v} }

// Bool returns a boolean-valued Attr.
func Bool(key string, v bool) Attr { return Attr{Key: key, Value: v} }

// Canonical attribute keys. Core emits these and the Convention layer maps
// them to gen_ai.* where a semantic convention exists, so a rename in a
// convention never touches core.
const (
	KeyFlow             = "gohan.flow"
	KeySessionID        = "gohan.session_id"
	KeyRunID            = "gohan.run_id"
	KeyRootRunID        = "gohan.root_run_id"
	KeyParentRunID      = "gohan.parent_run_id"
	KeyTurn             = "gohan.turn"
	KeyTenant           = "gohan.tenant"
	KeySubject          = "gohan.subject"
	KeyRelease          = "gohan.release"
	KeyVariant          = "gohan.variant"
	KeyModeAttr         = "gohan.mode"
	KeyModelProfile     = "gohan.model.profile"
	KeyModelVersion     = "gohan.model.version"
	KeyModelEndpoint    = "gohan.model.endpoint"
	KeyModelKeyID       = "gohan.model.key_id"
	KeyUsageInput       = "gohan.usage.input"
	KeyUsageCachedInput = "gohan.usage.cached_input"
	KeyUsageCacheWrite  = "gohan.usage.cache_write"
	KeyUsageOutput      = "gohan.usage.output"
	KeyUsageHedgeLoser  = "gohan.usage.hedge_loser"
	KeyCost             = "gohan.cost"
	KeyToolName         = "gohan.tool.name"
	KeyToolEffect       = "gohan.tool.effect"
	KeyToolOutcome      = "gohan.tool.outcome"
	KeyJournalReplayed  = "gohan.journal.replayed"
	KeyGuardStage       = "gohan.guard.stage"
	KeyGuardVerdict     = "gohan.guard.verdict"
	KeyRouterDecision   = "gohan.router.decision"
	KeyRouterConfidence = "gohan.router.confidence"
	KeyApprover         = "gohan.approver"
	KeySuspendReason    = "gohan.suspend.reason"
	KeyNoticeKind       = "gohan.notice.kind"
)

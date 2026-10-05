package gohan

import (
	"log/slog"

	"github.com/victorzhuk/gohan/core/types"
)

// The logging contract: message content, tool arguments and results,
// prompts, credentials and Raw values never reach a log at any level. The
// exclusion is structural, not a redaction pass - the run logger is built
// from the canonical attributes alone and no helper here accepts a
// content-bearing value, so there is nothing to filter out downstream.

// runLoggerAttrs are the canonical attributes a per-run logger carries:
// the run-tree keys the attribute table places on run, model and tool
// spans, plus the release identity every record is attributed to.
func runLoggerAttrs(info types.RunInfo) []slog.Attr {
	attrs := []slog.Attr{
		slog.String(types.KeyFlow, info.Flow),
		slog.String(types.KeySessionID, info.SessionID),
		slog.String(types.KeyRunID, info.RunID),
		slog.String(types.KeyRootRunID, info.RootRunID),
	}
	if info.ParentRunID != "" {
		attrs = append(attrs, slog.String(types.KeyParentRunID, info.ParentRunID))
	}
	attrs = append(attrs,
		slog.Int64(types.KeyTurn, int64(info.Turn)),
		modeAttr(info.Mode),
	)
	if info.Principal.Tenant != "" {
		attrs = append(attrs, slog.String(types.KeyTenant, info.Principal.Tenant))
	}
	if info.Principal.Subject != "" {
		attrs = append(attrs, slog.String(types.KeySubject, info.Principal.Subject))
	}
	if info.ReleaseID != "" {
		attrs = append(attrs, slog.String(types.KeyRelease, info.ReleaseID))
	}
	if info.Variant != "" {
		attrs = append(attrs, slog.String(types.KeyVariant, info.Variant))
	}
	return attrs
}

func modeAttr(m types.RunMode) slog.Attr {
	switch m {
	case types.Shadow:
		return slog.String(types.KeyModeAttr, "shadow")
	default:
		return slog.String(types.KeyModeAttr, "primary")
	}
}

// runLogger derives the per-run logger from the stack's logger, so the
// handler stays the one WithLogger installed. Content is excluded by
// construction: only the canonical attributes above are attached, and
// every record emitted through it names events, not payloads.
func runLogger(base *slog.Logger, info types.RunInfo) *slog.Logger {
	attrs := runLoggerAttrs(info)
	args := make([]any, len(attrs))
	for i, a := range attrs {
		args[i] = a
	}
	return base.With(args...)
}

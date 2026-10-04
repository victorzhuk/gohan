package gohan

import (
	"log/slog"
)

// matrixEntry is one flow × profile × strategy resolution the build
// resolved. The names stay internal: the record shape is the spec's
// prose rule, not a declared contract.
type matrixEntry struct {
	Flow             string
	Profile          string
	Fallback         string
	Structured       string
	ParallelTools    bool
	MaxParallelTools int
}

func entryName(s StructuredStrategy) string {
	switch s {
	case StructuredConstrained:
		return "constrained"
	case StructuredToolSchema:
		return "tool_schema"
	default:
		return "none"
	}
}

// logResolvedMatrix writes the one structured record that lists every
// flow × profile × strategy resolution (build.resolved-matrix). Build
// logs it once at startup, at Info, and never emits a metric for it.
func logResolvedMatrix(l *slog.Logger, entries []matrixEntry) {
	if l == nil {
		return
	}
	attrs := make([]any, 0, len(entries))
	for _, e := range entries {
		attrs = append(attrs, slog.Any(e.Flow, e))
	}
	l.Info("resolved strategy matrix", attrs...)
}

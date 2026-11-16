package evals

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/telemetry"
)

// ErrImportRedactorRequired refuses a production-session import without a
// redactor: recorded sessions carry real subjects.
var ErrImportRedactorRequired = errors.New("gohan/evals: importing a production session requires a redactor")

// ImportOption adjusts Import.
type ImportOption func(*importSettings)

type importSettings struct {
	redactor types.Redactor
	policy   types.RedactionPolicy
}

// WithImportRedactor binds the redactor the import runs every block
// through before the case leaves the process.
func WithImportRedactor(r types.Redactor, p types.RedactionPolicy) ImportOption {
	return func(s *importSettings) { s.redactor, s.policy = r, p }
}

// Import turns one recorded production session into the replayable cases
// a shadow run consumes: input and expected output hold the redacted
// text, and each case's tags carry the original tool sequence in call
// order.
func Import(ctx context.Context, h stores.History, opts ...ImportOption) ([]Case, error) {
	var s importSettings
	for _, opt := range opts {
		opt(&s)
	}
	if s.redactor == nil {
		return nil, ErrImportRedactorRequired
	}
	var tools []string
	for _, msg := range h.Messages {
		for _, b := range msg.Blocks {
			if use, ok := b.(types.ToolUse); ok {
				tools = append(tools, use.Name)
			}
		}
	}
	messages := make([]types.Message, len(h.Messages))
	for i, msg := range h.Messages {
		redacted, _, err := s.redactor.Redact(ctx, msg.Blocks, s.policy)
		if err != nil {
			return nil, fmt.Errorf("gohan/evals: redact imported session: %w", err)
		}
		messages[i] = types.Message{ID: msg.ID, Role: msg.Role, Blocks: redacted, Meta: msg.Meta}
	}
	var in, expected string
	for _, msg := range messages {
		if msg.Role == types.RoleUser && in == "" {
			in = textOf(msg.Blocks)
		}
		if msg.Role == types.RoleAssistant {
			expected = textOf(msg.Blocks)
		}
	}
	return []Case{{
		Input:    in,
		Expected: expected,
		Tags:     tools,
	}}, nil
}

func textOf(blocks []types.Block) string {
	var sb strings.Builder
	for _, b := range blocks {
		if t, ok := b.(types.Text); ok {
			sb.WriteString(t.Text)
		}
	}
	return sb.String()
}

// Metrics returns the metric registrations online evaluation carries.
func Metrics() []telemetry.Metric {
	return []telemetry.Metric{
		{Name: telemetry.MetricEvalScore, Labels: []string{"flow", "judge"}},
		{Name: telemetry.MetricEvalUnstable, Labels: []string{"flow", "judge"}},
	}
}

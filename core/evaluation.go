package gohan

import (
	"context"
	"errors"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// RunEvaluator is the terminal Done seam online evaluation samples
// finished primary runs through. The lifecycle calls it after the terminal
// store write, beside consolidation (core/lifecycle_terminal.go). Evaluate
// admits work without blocking: it mints identities, counts and enqueues,
// and returns without waiting for any judge, so the terminal Done is never
// delayed. An error is logged, never a failed Done. Evaluation jobs cannot
// recursively trigger evaluation: they call models directly and never
// construct a Conversation or Flow.
type RunEvaluator interface {
	Evaluate(ctx context.Context, c RunCompletion) error
}

// RunCompletion is the immutable completion snapshot the seam receives.
// ctx is the request context; implementations must not retain it beyond
// the Evaluate call. History loads the session history on the evaluator's
// own context; a store that has since advanced or compacted returns its
// current history, which evaluation records as missing evidence rather
// than failing. A nil History settles every dimension unscored with
// category missing_evidence.
type RunCompletion struct {
	Info    types.RunInfo
	Done    types.Done
	Version int64
	History func(ctx context.Context) (stores.History, error)
}

// HistoryLoader loads a session's history as of a completion version. The
// lifecycle binds it over the conversation's session log.
type HistoryLoader func(ctx context.Context, version int64) (stores.History, error)

// WithRunEvaluator binds the online evaluation engine every conversation
// of this stack samples finished primary runs into. A build without one
// evaluates nothing.
func WithRunEvaluator(e RunEvaluator) Option {
	return func(c *config) error {
		if e == nil {
			return errors.New("gohan: WithRunEvaluator needs a non-nil evaluator")
		}
		c.evaluator = e
		return nil
	}
}

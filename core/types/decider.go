package types

import "context"

// Decision carries the value a decider chose and how certain it is.
// Confidence is in [0, 1]; rules-based deciders always report 1.
type Decision[D any] struct {
	Value      D
	Confidence float64
}

// Decider maps a state to a decision. Errors fail closed at the call site.
type Decider[S, D any] interface {
	Decide(ctx context.Context, state S) (Decision[D], error)
}

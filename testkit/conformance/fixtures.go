// Package conformance holds the reusable suites a capability implementation
// runs to prove it honours the gohan contracts: error classes, usage
// accounting, streaming order, cancellation and pass-through fidelity. A
// suite takes the implementation as an argument, so a scripted double and a
// real provider adapter run the same checks. The Model suite and the
// provider fixture set live in model.go and fixtures.go; the Runtime, Chain
// and Flow suites are added by their own files in this package.
package conformance

import (
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// FixtureKind selects the contract property a fixture exercises.
type FixtureKind int

const (
	// FixtureError checks the failure mapping: the adapter must surface the
	// recorded provider failure as a *ModelError with the fixture's class.
	FixtureError FixtureKind = iota + 1
	// FixtureUsage checks the usage contract on a successful stream.
	FixtureUsage
	// FixtureTruncation checks a max_tokens finish that ends an open tool
	// call: the tool use must be complete and the finish carried through.
	FixtureTruncation
	// FixtureRawRoundTrip checks that a Raw block the model does not
	// interpret travels through a call unchanged and without error.
	FixtureRawRoundTrip
)

// Fixture is one recorded provider response in model-level terms. Status,
// Code and RetryAfter describe what the provider sent; the adapter under
// test maps them onto the model-level outcome the suite asserts. A fixture
// is data: no network call, no clock, no goroutine.
type Fixture struct {
	Name       string
	Kind       FixtureKind
	Status     int
	Code       string
	RetryAfter time.Duration
	Class      types.ErrorClass
	Text       string
	ToolName   string
	Usage      *types.Usage
	Raw        types.Raw
}

// Fixtures is the fixture set an adapter ships with its conformance run.
type Fixtures []Fixture

// FixtureKey marks a request as fixture playback. An adapter maps the named
// fixture to the recorded provider response instead of calling the network.
const FixtureKey = "gohan.conformance.fixture"

// DefaultFixtures returns the fixture set every provider adapter ships:
// the status-to-class table with Retry-After, the usage contract including
// the no-usage estimate rule, a max_tokens truncation mid tool call and the
// unmodelled-block Raw round trip.
func DefaultFixtures() Fixtures {
	served := "served-1"
	return Fixtures{
		{
			Name:       "rate limited with retry after",
			Kind:       FixtureError,
			Status:     429,
			RetryAfter: 7 * time.Second,
			Class:      types.ClassRateLimited,
		},
		{
			Name:   "quota code",
			Kind:   FixtureError,
			Status: 400,
			Code:   "insufficient_quota",
			Class:  types.ClassRateLimited,
		},
		{
			Name:   "transient",
			Kind:   FixtureError,
			Status: 500,
			Class:  types.ClassTransient,
		},
		{
			Name:   "auth",
			Kind:   FixtureError,
			Status: 401,
			Class:  types.ClassAuth,
		},
		{
			Name:   "permanent",
			Kind:   FixtureError,
			Status: 400,
			Class:  types.ClassPermanent,
		},
		{
			Name:   "context overflow",
			Kind:   FixtureError,
			Status: 400,
			Code:   "context_length_exceeded",
			Class:  types.ClassContextOverflow,
		},
		{
			Name:   "deprecated",
			Kind:   FixtureError,
			Status: 404,
			Code:   "model_not_found",
			Class:  types.ClassDeprecated,
		},
		{
			Name: "usage reported",
			Kind: FixtureUsage,
			Text: "hello",
			Usage: &types.Usage{
				InputTokens:       100,
				CachedInputTokens: 20,
				OutputTokens:      30,
				CacheWriteTokens:  15,
				ModelVersion:      served,
			},
		},
		{
			Name: "usage estimated",
			Kind: FixtureUsage,
			Text: "hello",
		},
		{
			Name:     "truncated tool call",
			Kind:     FixtureTruncation,
			ToolName: "search",
		},
		{
			Name: "raw round trip",
			Kind: FixtureRawRoundTrip,
			Raw:  types.Raw{Provider: "acme", Value: map[string]any{"acme_kind": "trace"}},
		},
	}
}

// Kinds reports the fixture kinds the set covers.
func (fs Fixtures) Kinds() map[FixtureKind]bool {
	kinds := make(map[FixtureKind]bool, len(fs))
	for _, fx := range fs {
		kinds[fx.Kind] = true
	}
	return kinds
}

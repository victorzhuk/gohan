// Package guards holds the guard contracts shared by the driver and the
// std policy packages. The stage enum and the blocked error live in
// core/types: they predate this package and are referenced, not redeclared.
package guards

import (
	"context"

	"github.com/victorzhuk/gohan/core/types"
)

// GuardAction is what a guard decided about the guarded content.
type GuardAction int

const (
	// Pass lets the content through unchanged.
	Pass GuardAction = iota
	// Rewrite replaces the content and continues.
	Rewrite
	// Block rejects the content.
	Block
)

// GuardVerdict is the decision value a guard returns.
type GuardVerdict struct {
	Action GuardAction
	// Blocks carries the rewritten content when Action is Rewrite.
	Blocks []types.Block
	Reason string
}

// GuardInput is the state a guard decides on.
type GuardInput struct {
	Stage  types.GuardStage
	Blocks []types.Block
	Blobs  []types.Blob
	Origin types.Origin
	Run    types.RunInfo
}

// Guard decides on guarded content at one stage. Errors fail closed at
// the call site.
type Guard = types.Decider[GuardInput, GuardVerdict]

// OutputMode selects buffered or windowed output guarding. Buffered with
// Window 0 is the unguarded-in-effect default for intermediate turns.
type OutputMode struct {
	Buffered bool
	Window   int
}

// Fallback answers a GuardBlockedError with the message the caller sees
// instead of the blocked content.
type Fallback func(ctx context.Context, err *types.GuardBlockedError) types.Message

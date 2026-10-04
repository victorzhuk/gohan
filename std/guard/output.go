package guard

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/victorzhuk/gohan/core/guards"
	"github.com/victorzhuk/gohan/core/types"
)

// DefaultWindow is the window size the Interactive profile maps to.
const DefaultWindow = 64

// OutputGuard guards the user-facing output of a turn. Guard returns the
// blocks the caller may release, or a *types.GuardBlockedError carrying
// the fallback message.
type OutputGuard interface {
	Name() string
	Guard(ctx context.Context, blocks []types.Block) ([]types.Block, error)
}

// intermediateTurn reports whether a turn consists only of tool calls or
// tool results. Output guards never run on such turns: the user never
// sees them.
func intermediateTurn(blocks []types.Block) bool {
	if len(blocks) == 0 {
		return false
	}
	for _, b := range blocks {
		switch b.(type) {
		case types.ToolUse, types.ToolResult:
		default:
			return false
		}
	}
	return true
}

// Buffered guards the complete answer once before anything is released.
type Buffered struct {
	Inner    guards.Guard
	Fallback guards.Fallback
}

// NewBuffered builds a buffered output guard.
func NewBuffered(inner guards.Guard, fallback guards.Fallback) *Buffered {
	return &Buffered{Inner: inner, Fallback: fallback}
}

// Name reports the decider name recorded by decision observability.
func (b *Buffered) Name() string { return "output-buffered" }

// Guard implements OutputGuard.
func (b *Buffered) Guard(ctx context.Context, blocks []types.Block) ([]types.Block, error) {
	if intermediateTurn(blocks) {
		return blocks, nil
	}
	return release(ctx, b.Inner, blocks, b.Fallback)
}

// Windowed guards an answer window by window: it holds back Window tokens,
// guards each full window before release, and cuts the stream on the first
// Block. A partial window is released after Idle without new chunks. The
// Done(guard_blocked) emission after the fallback message belongs to the
// conversation stream, not to this guard.
type Windowed struct {
	Inner    guards.Guard
	Window   int
	Idle     time.Duration
	Fallback guards.Fallback
}

// NewWindowed builds a windowed output guard with the given window size.
func NewWindowed(inner guards.Guard, window int, fallback guards.Fallback) *Windowed {
	if window <= 0 {
		window = DefaultWindow
	}
	return &Windowed{Inner: inner, Window: window, Fallback: fallback}
}

// Name reports the decider name recorded by decision observability.
func (w *Windowed) Name() string { return "output-windowed" }

// GuardStream consumes chunks, guards and emits them window by window, and
// emits the fallback message before returning a *types.GuardBlockedError.
func (w *Windowed) GuardStream(ctx context.Context, chunks <-chan types.Text, emit func(types.Text) error) error {
	var pending []types.Text
	count := 0
	var timer *time.Timer
	if w.Idle > 0 {
		timer = time.NewTimer(w.Idle)
		defer func() {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}()
	}
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		parts := make([]string, len(pending))
		for i, t := range pending {
			parts[i] = t.Text
		}
		pending = nil
		count = 0
		blocks, err := release(ctx, w.Inner, []types.Block{types.Text{Text: strings.Join(parts, " ")}}, w.Fallback)
		if err != nil {
			var gbe *types.GuardBlockedError
			if errors.As(err, &gbe) && gbe.Fallback.Blocks != nil {
				for _, b := range gbe.Fallback.Blocks {
					if err := emit(b.(types.Text)); err != nil {
						return err
					}
				}
			}
			return err
		}
		for _, b := range blocks {
			if err := emit(b.(types.Text)); err != nil {
				return err
			}
		}
		return nil
	}
	for {
		if timer != nil && count > 0 {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(w.Idle)
		}
		var timerC <-chan time.Time
		if timer != nil && count > 0 {
			timerC = timer.C
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timerC:
			if err := flush(); err != nil {
				return err
			}
		case chunk, ok := <-chunks:
			if !ok {
				return flush()
			}
			pending = append(pending, chunk)
			count += tokenCount(chunk.Text)
			if count >= w.Window {
				if err := flush(); err != nil {
					return err
				}
			}
		}
	}
}

func tokenCount(s string) int { return len(strings.Fields(s)) }

// release decides on the given blocks: a Pass releases them unchanged, a
// Rewrite releases the replacement, and a Block emits nothing and fails
// with the guarded error carrying the fallback message.
func release(ctx context.Context, inner guards.Guard, blocks []types.Block, fallback guards.Fallback) ([]types.Block, error) {
	d, err := inner.Decide(ctx, guards.GuardInput{Stage: types.StageOutput, Blocks: blocks})
	if err != nil {
		return nil, err
	}
	switch d.Value.Action {
	case guards.Block:
		gbe := &types.GuardBlockedError{Stage: types.StageOutput, Reason: d.Value.Reason}
		if fallback != nil {
			gbe.Fallback = fallback(ctx, gbe)
		}
		return nil, gbe
	case guards.Rewrite:
		return d.Value.Blocks, nil
	default:
		return blocks, nil
	}
}

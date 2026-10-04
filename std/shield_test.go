package std

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"testing/synctest"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

func TestCancelShield(t *testing.T) {
	t.Run("streams.disconnect-during-side-effect", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			j := &countingJournal{Journal: stores.NewMemoryJournal()}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var innerErr error
			tool := chains.ToolFunc(func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
				cancel()
				if err := ctx.Err(); err != nil {
					innerErr = err
				}
				return types.ToolResult{Outcome: types.Succeeded}, nil
			})
			chain := Shield(lookupOf(bookingSpecs()))(Journal(j, WithJournalSpecs(lookupOf(bookingSpecs())))(tool))
			res, err := chain(ctx, toolCall("create_booking", "c1", `{"room":7}`))
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("err = %v, want context.Canceled", err)
			}
			if res.Outcome != types.Succeeded {
				t.Fatalf("result outcome = %v, want the shielded tool result", res.Outcome)
			}
			if innerErr != nil {
				t.Fatalf("tool saw cancellation: %v", innerErr)
			}
			entries, _ := j.ByFingerprint(context.Background(), "", CanonicalFingerprint("create_booking", json.RawMessage(`{"room":7}`)))
			if len(entries) != 1 || entries[0].State != stores.Completed || entries[0].Result.Outcome != types.Succeeded {
				t.Fatalf("journal = %+v, want c1 Completed Succeeded", entries)
			}
		})
	})

	t.Run("chains.read-only-tools-pay-nothing", func(t *testing.T) {
		j := &countingJournal{Journal: stores.NewMemoryJournal()}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		var sawCancel bool
		tool := chains.ToolFunc(func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
			sawCancel = ctx.Err() != nil
			return types.ToolResult{Outcome: types.Succeeded}, ctx.Err()
		})
		chain := Shield(lookupOf(bookingSpecs()))(Journal(j, WithJournalSpecs(lookupOf(bookingSpecs())))(tool))
		_, err := chain(ctx, toolCall("get_booking", "c1", `{"id":"b1"}`))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		if !sawCancel {
			t.Fatal("read-only tool was shielded from cancellation")
		}
		if j.reserves != 0 || j.completes != 0 {
			t.Fatalf("journal ops = reserve %d, complete %d, want 0/0", j.reserves, j.completes)
		}
	})
}

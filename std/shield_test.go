package std

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"testing"
	"testing/synctest"
	"time"

	gohan "github.com/victorzhuk/gohan/core"
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
		t.Run("chain skips journal and shield", func(t *testing.T) {
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

		t.Run("native turn journals nothing and records history", func(t *testing.T) {
			j := &countingJournal{Journal: stores.NewMemoryJournal()}
			log := &countingSessionLog{MemorySessionLog: stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))}
			stack, err := gohan.Build(
				gohan.WithStores(stores.Stores{SessionLog: log, Journal: j}),
				gohan.WithModels(&readOnlyTurnModel{}),
				gohan.WithNativeAgent(gohan.NativeSpec{
					Request: gohan.FlowRequest{Name: "read-only"},
					Profile: "native",
					Tools:   []types.Tool{readOnlyLookupTool{}},
					Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
						return types.ModelRequest{Messages: append(append([]types.Message{}, in.History...), in.Input...)}, nil
					},
				}),
			)
			if err != nil {
				t.Fatalf("build: %v", err)
			}
			conv, err := gohan.NewNativeConversation(stack, "read-only",
				gohan.WithConversationRuns(stores.NewMemoryRuns(
					stores.WithMemoryRunClock(time.Now),
					stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
						p, ok := types.PrincipalFrom(ctx)
						return types.RunInfo{Principal: p}, ok
					}),
				)),
				gohan.WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
			)
			if err != nil {
				t.Fatalf("new native conversation: %v", err)
			}
			ctx := types.WithPrincipal(context.Background(), types.Principal{Tenant: "t", Subject: "u"})
			var runErr error
			for _, err := range conv.Send(ctx, "read-only-1", types.Message{
				Role:   types.RoleUser,
				Blocks: []types.Block{types.Text{Text: "show booking b1"}},
			}) {
				if err != nil {
					runErr = err
				}
			}
			if runErr != nil {
				t.Fatalf("send: %v", runErr)
			}
			if j.reserves != 0 || j.completes != 0 {
				t.Fatalf("journal ops = reserve %d, complete %d, want 0/0", j.reserves, j.completes)
			}
			if log.appends != 3 {
				t.Fatalf("session log appends = %d, want 3: one user input, one read-only batch settlement entry, one final assistant entry", log.appends)
			}
		})
	})
}

// countingSessionLog counts history appends so a turn's durable write
// volume stays observable.
type countingSessionLog struct {
	*stores.MemorySessionLog
	appends int
}

func (l *countingSessionLog) Append(ctx context.Context, sessionID string, expectedVersion int64, msgs ...types.Message) (int64, error) {
	l.appends++
	return l.MemorySessionLog.Append(ctx, sessionID, expectedVersion, msgs...)
}

// readOnlyTurnModel calls the read-only lookup once, then answers.
type readOnlyTurnModel struct{ calls int }

func (m *readOnlyTurnModel) Profile() types.ModelProfile {
	return types.ModelProfile{Name: "native", Caps: types.Caps{Tools: true}}
}

func (m *readOnlyTurnModel) Generate(_ context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	m.calls++
	return func(yield func(types.ModelChunk, error) bool) {
		if m.calls == 1 {
			yield(types.ModelChunk{Kind: types.DeltaToolArgs, ToolUse: &types.ToolUse{ID: "c1", Name: "get_booking", Args: json.RawMessage(`{"id":"b1"}`)}}, nil)
			yield(types.ModelChunk{Finish: types.FinishToolUse}, nil)
			return
		}
		yield(types.ModelChunk{Kind: types.DeltaText, Delta: "b1 is confirmed"}, nil)
		yield(types.ModelChunk{Finish: types.FinishStop}, nil)
	}
}

// readOnlyLookupTool is a ReadOnly lookup the preset journal and shield
// must both exempt.
type readOnlyLookupTool struct{}

func (readOnlyLookupTool) Spec() types.ToolSpec {
	return types.ToolSpec{
		Name:     "get_booking",
		Schema:   json.RawMessage(`{"type":"object"}`),
		Effect:   types.ReadOnly,
		Executor: types.ByHarness,
	}
}

func (readOnlyLookupTool) Call(context.Context, json.RawMessage) (types.ToolResult, error) {
	return types.ToolResult{Outcome: types.Succeeded}, nil
}

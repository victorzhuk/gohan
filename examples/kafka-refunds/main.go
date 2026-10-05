// Command kafka-refunds demonstrates the engine-composition contract for
// a Kafka-style consumer over gohan's governed native conversation,
// offline: one delivered event drives one bounded run, the governed path
// journals the refund and persists the history, and a redelivery of the
// same message returns the recorded result instead of refunding twice. A
// live kill flag consulted after approval denies the refund without
// executing it. No Kafka client and no network: the fixture feeds the
// events.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std"
)

// consumer is the refunds worker. The governed conversation owns the run
// row, the journal and the persisted history; the consumer maps one
// delivered message onto one send and reads the recorded result back from
// the stores the governed path wrote.
type consumer struct {
	conv     gohan.Conversation
	runs     *stores.MemoryRuns
	journal  *stores.MemoryJournal
	sessions *stores.MemorySessionLog
	audit    *stores.MemoryAuditLog
}

// ErrRedelivered reports that the operation id was already recorded and
// carries the existing run id and the result the first delivery stored.
type ErrRedelivered struct {
	RunID  string
	Result string
}

func (e ErrRedelivered) Error() string {
	return "gohan: operation id already recorded"
}

func newConsumer(do refundTool, kill func() bool) (*consumer, error) {
	c := &consumer{
		runs: stores.NewMemoryRuns(stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
			p, ok := types.PrincipalFrom(ctx)
			if !ok {
				return types.RunInfo{}, false
			}
			return types.RunInfo{Principal: p}, true
		})),
		journal:  stores.NewMemoryJournal(),
		sessions: stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)),
		audit:    stores.NewMemoryAuditLog(),
	}
	stack, err := gohan.Build(
		gohan.WithStores(stores.Stores{SessionLog: c.sessions, Journal: c.journal}),
		gohan.WithModels(&refundScript{}),
		gohan.WithNativeAgent(gohan.NativeSpec{
			Request: gohan.FlowRequest{Name: "refunds"},
			Profile: "scripted",
			Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
				return types.ModelRequest{Messages: slices.Clone(in.History)}, nil
			},
			Tools: []types.Tool{refundCall{do: do}},
			ToolChain: chains.ToolChain{{
				Name: "journal",
				Kind: chains.KindJournal,
				Use: std.Journal(c.journal,
					std.WithJournalSpecs(refundSpecLookup),
					std.WithJournalSessionID(deliverySession),
				),
			}},
			Decider: liveKillDecider{audit: c.audit, kill: kill},
		}),
	)
	if err != nil {
		return nil, err
	}
	conv, err := gohan.NewNativeConversation(stack, "refunds",
		gohan.WithConversationRuns(c.runs),
		gohan.WithConversationEventLog(stores.NewMemoryEventLog()),
	)
	if err != nil {
		return nil, err
	}
	c.conv = conv
	return c, nil
}

// handle processes one delivered message. A first delivery sends the
// message through the governed flow: approve, consult the live kill flag,
// execute the refund once, journal it. A redelivery of the same message
// key is refused off the run row before any component runs, and the
// recorded result replays instead of the effect.
func (c *consumer) handle(ctx context.Context, ev RefundEvent) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// The message key is the OperationID: the run row is keyed by it, so
	// identical business work across redeliveries resolves to one run.
	if run, err := c.runs.ByOperation(ctx, ev.Tenant(), ev.Key); err == nil {
		return c.recorded(ctx, ev), ErrRedelivered{RunID: run.RunID, Result: c.recorded(ctx, ev)}
	} else if !errors.Is(err, stores.ErrRunNotFound) {
		return "", err
	}
	ctx = types.WithPrincipal(ctx, types.Principal{
		Tenant:  ev.Tenant(),
		Subject: "refunds-worker",
	})
	ctx = types.WithIdempotencyKey(ctx, ev.Key)
	ctx = withDelivery(ctx, ev)
	for _, err := range c.conv.Send(ctx, ev.SessionID(), types.Message{
		Role:   types.RoleUser,
		Blocks: []types.Block{types.Text{Text: ev.request()}},
	}) {
		if err != nil {
			return "", err
		}
	}
	return c.recorded(ctx, ev), nil
}

// recorded reads back what the first delivery stored: a completed journal
// entry for the refund call, or the not-executed result the governed path
// persisted when the live flag denied the call.
func (c *consumer) recorded(ctx context.Context, ev RefundEvent) string {
	res, err := replayedResult(c.journal, ev)
	if err != nil || res != "" {
		return res
	}
	hist, err := c.sessions.Load(ctx, ev.SessionID())
	if err != nil {
		return ""
	}
	for _, m := range hist.Messages {
		for _, b := range m.Blocks {
			r, ok := b.(types.ToolResult)
			if !ok || r.ID != "refund" {
				continue
			}
			if r.Error != nil {
				return "denied: " + r.Error.Message
			}
			return resultText(r)
		}
	}
	return ""
}

func resultText(res types.ToolResult) string {
	for _, b := range res.Content {
		if t, ok := b.(types.Text); ok {
			return t.Text
		}
	}
	return ""
}

func main() {
	d, err := newOfflineDeliverer(func() bool { return false })
	if err != nil {
		fmt.Fprintln(os.Stderr, "kafka-refunds:", err)
		os.Exit(1)
	}
	ev := RefundEvent{Key: "ord-42", OrderID: "ord-42", Cents: 2500}
	first, err := d.deliver(ev)
	if err != nil {
		fmt.Fprintln(os.Stderr, "kafka-refunds: first delivery:", err)
		os.Exit(1)
	}
	if d.executes(ev.OrderID) != 1 {
		fmt.Fprintln(os.Stderr, "kafka-refunds: first delivery did not execute the refund exactly once")
		os.Exit(1)
	}
	again, err := d.deliver(ev)
	if _, ok := errors.AsType[ErrRedelivered](err); !ok {
		fmt.Fprintln(os.Stderr, "kafka-refunds: redelivery error:", err)
		os.Exit(1)
	}
	if again != first {
		fmt.Fprintf(os.Stderr, "kafka-refunds: redelivery returned %q, want the recorded %q\n", again, first)
		os.Exit(1)
	}
	if d.executes(ev.OrderID) != 1 {
		fmt.Fprintln(os.Stderr, "kafka-refunds: redelivery executed the refund again")
		os.Exit(1)
	}
	fmt.Printf("refund recorded: %s\n", first)
	fmt.Printf("redelivery replayed the recorded result; refund executed %d time\n", d.executes(ev.OrderID))
}

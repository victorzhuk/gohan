package main

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"iter"
	"sync"

	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std"
)

// RefundEvent is one delivered message. Key is the Kafka message key and
// doubles as the gohan OperationID, so a redelivery of the same message
// deduplicates against the first delivery's run.
type RefundEvent struct {
	Key     string
	OrderID string
	Cents   int
}

// Tenant is the message's partition tenant; a real consumer maps it from
// the record headers.
func (ev RefundEvent) Tenant() string { return "acme" }

// SessionID namespaces the run and its journal entries per message key.
func (ev RefundEvent) SessionID() string { return "refunds-" + ev.Key }

// RunID names the run one delivery drives; the governed conversation mints
// the stored run id, this only keys the audit trail.
func (ev RefundEvent) RunID() string { return "run-" + ev.Key }

// request is the text the delivered message carries into the flow.
func (ev RefundEvent) request() string {
	return fmt.Sprintf("refund %s %d", ev.OrderID, ev.Cents)
}

// deliverer feeds events to the consumer in delivery order, including
// redeliveries, and records the executed refunds so tests can observe the
// side effect.
type deliverer struct {
	c *consumer

	mu       sync.Mutex
	refunded map[string]int
}

func newDeliverer(c *consumer) *deliverer {
	return &deliverer{c: c, refunded: map[string]int{}}
}

// newOfflineDeliverer wires a consumer over d.settle with the given kill
// flag, so tests observe the executed side effect.
func newOfflineDeliverer(kill func() bool) (*deliverer, error) {
	d := newDeliverer(nil)
	c, err := newConsumer(d.settle, kill)
	if err != nil {
		return nil, err
	}
	d.c = c
	return d, nil
}

// replayedResult reads the journaled result for the event's refund call
// back from the journal, the way a retried effect replays it.
func replayedResult(j *stores.MemoryJournal, ev RefundEvent) (string, error) {
	fp := std.CanonicalFingerprint("refund", refundArgs(ev))
	entries, err := j.ByFingerprint(context.Background(), ev.SessionID(), fp)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.State == stores.Completed {
			return resultText(e.Result), nil
		}
	}
	return "", nil
}

// replay reads the journaled result for the event's refund call back from
// the journal, the way a retried effect replays it.
func (d *deliverer) replay(ev RefundEvent) (string, error) {
	return replayedResult(d.c.journal, ev)
}

// deliver hands one message to the consumer, like a Kafka poll would.
func (d *deliverer) deliver(ev RefundEvent) (string, error) {
	ctx := types.WithPrincipal(context.Background(), types.Principal{
		Tenant:  ev.Tenant(),
		Subject: "refunds-worker",
	})
	return d.c.handle(ctx, ev)
}

// executes counts how often the refund side effect ran for an order.
func (d *deliverer) executes(orderID string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.refunded[orderID]
}

// settle is the refund side effect the governed flow journals.
func (d *deliverer) settle(orderID string, cents int) (string, error) {
	d.mu.Lock()
	d.refunded[orderID]++
	d.mu.Unlock()
	return fmt.Sprintf("refunded %d cents for %s", cents, orderID), nil
}

// refundTool is the journaled side effect; a live refunding consumer would
// call the payment provider here.
type refundTool func(orderID string, cents int) (string, error)

// refundCall adapts the refund side effect to the governed tool surface.
type refundCall struct {
	do refundTool
}

func (t refundCall) Spec() types.ToolSpec {
	return types.ToolSpec{
		Name:        "refund",
		Description: "Refund a delivered order.",
		Effect:      types.SideEffect,
		Risk:        types.RiskMedium,
	}
}

func (t refundCall) Call(_ context.Context, args json.RawMessage) (types.ToolResult, error) {
	var in struct {
		OrderID     string `json:"order_id"`
		AmountCents int    `json:"amount_cents"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return types.ToolResult{}, err
	}
	out, err := t.do(in.OrderID, in.AmountCents)
	if err != nil {
		return types.ToolResult{}, err
	}
	return types.ToolResult{
		ID:      "refund",
		Outcome: types.Succeeded,
		Content: []types.Block{types.Text{Text: out}},
	}, nil
}

func refundArgs(ev RefundEvent) json.RawMessage {
	args, err := json.Marshal(map[string]any{"order_id": ev.OrderID, "amount_cents": ev.Cents})
	if err != nil {
		return nil
	}
	return args
}

// deliveryKey carries one delivered message through the send's context, so
// the flow's decider can key its audit records to the delivery.
type deliveryKey struct{}

func withDelivery(ctx context.Context, ev RefundEvent) context.Context {
	return context.WithValue(ctx, deliveryKey{}, ev)
}

// liveKillDecider is the flow's tool policy: the operator's approval is
// recorded, then the live refunds.kill flag is consulted. A raised flag
// beats the approval: the call is denied and never executes.
type liveKillDecider struct {
	audit *stores.MemoryAuditLog
	kill  func() bool
}

func (d liveKillDecider) Decide(ctx context.Context, inv *permission.ToolInvocation) (types.Decision[permission.Verdict], error) {
	ev, _ := ctx.Value(deliveryKey{}).(RefundEvent)
	_ = d.audit.Append(ctx, stores.AuditRecord{
		Kind:      stores.AuditApproval,
		SessionID: ev.SessionID(),
		RunID:     ev.RunID(),
		Flow:      "refunds",
		Tool:      inv.Spec.Name,
		Approver:  "ops-oncall",
		Verdict:   "approved",
	})
	if d.kill() {
		_ = d.audit.Append(ctx, stores.AuditRecord{
			Kind:      stores.AuditToolDecision,
			SessionID: ev.SessionID(),
			RunID:     ev.RunID(),
			Flow:      "refunds",
			Tool:      inv.Spec.Name,
			Decision:  "flag_denied",
		})
		return types.Decision[permission.Verdict]{Value: permission.DenyVerdict, Confidence: 1}, nil
	}
	_ = d.audit.Append(ctx, stores.AuditRecord{
		Kind:      stores.AuditToolDecision,
		SessionID: ev.SessionID(),
		RunID:     ev.RunID(),
		Flow:      "refunds",
		Tool:      inv.Spec.Name,
		Decision:  "allowed",
	})
	return types.Decision[permission.Verdict]{Value: permission.Allow, Confidence: 1}, nil
}

// refundScript scripts the refund flow: the first turn calls refund with
// the delivered order, the second turn closes the run with a fixed reply.
type refundScript struct{}

func (m *refundScript) Profile() types.ModelProfile { return types.ModelProfile{Name: "scripted"} }

func (m *refundScript) Generate(_ context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		for _, c := range m.turn(req) {
			if !yield(c, nil) {
				return
			}
		}
	}
}

// turn inspects the assembled history: once the refund call has a result
// in history the script emits its closing text, otherwise the call. The
// script is stateless, so one model instance serves every delivery.
func (m *refundScript) turn(req types.ModelRequest) []types.ModelChunk {
	for _, msg := range req.Messages {
		for _, b := range msg.Blocks {
			if res, ok := b.(types.ToolResult); ok && res.ID == "refund" {
				return []types.ModelChunk{
					{Kind: types.DeltaText, Delta: "refund request handled"},
					{Finish: types.FinishStop},
				}
			}
		}
	}
	var text string
	for _, b := range req.Messages[len(req.Messages)-1].Blocks {
		if t, ok := b.(types.Text); ok {
			text += t.Text
		}
	}
	ev, ok := parseRequest(text)
	if !ok {
		return []types.ModelChunk{
			{Kind: types.DeltaText, Delta: "malformed refund request"},
			{Finish: types.FinishStop},
		}
	}
	call := types.ToolUse{ID: "refund", Name: "refund", Args: jsontextArgs(string(refundArgs(ev)))}
	return []types.ModelChunk{{ToolUse: &call, Finish: types.FinishToolUse}}
}

func parseRequest(text string) (RefundEvent, bool) {
	var ev RefundEvent
	if _, err := fmt.Sscanf(text, "refund %s %d", &ev.OrderID, &ev.Cents); err != nil {
		return RefundEvent{}, false
	}
	return ev, true
}

func jsontextArgs(s string) (v jsontext.Value) {
	return jsontext.Value(s)
}

func refundSpecLookup(name string) (types.ToolSpec, bool) {
	if name == "refund" {
		return refundCall{}.Spec(), true
	}
	return types.ToolSpec{}, false
}

func deliverySession(ctx context.Context) string {
	ev, _ := ctx.Value(deliveryKey{}).(RefundEvent)
	return ev.SessionID()
}

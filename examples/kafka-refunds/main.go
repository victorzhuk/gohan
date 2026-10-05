// Command kafka-refunds demonstrates the engine-composition contract for
// a Kafka-style consumer, offline: one delivered event drives one bounded
// run, the refund side effect is journaled, and a redelivery of the same
// message returns the recorded result instead of refunding twice. A live
// kill flag consulted after approval denies the refund without executing
// it. No Kafka client and no network: the fixture feeds the events.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// refundTool is the journaled side effect; a live refunding consumer would
// call the payment provider here.
type refundTool func(orderID string, cents int) (string, error)

// consumer is the refunds worker. It owns the gohan concerns from the
// engine contract: the run row (OperationID dedup), the journal (effect
// replay) and the audit trail.
type consumer struct {
	runs    *stores.MemoryRuns
	journal *stores.MemoryJournal
	audit   *stores.MemoryAuditLog
	// kill refunds.live, read at effect time so an operator can flip it
	// while a run is in flight.
	kill func() bool
	do   refundTool
}

func newConsumer(do refundTool, kill func() bool) *consumer {
	runs := stores.NewMemoryRuns(stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
		p, ok := types.PrincipalFrom(ctx)
		if !ok {
			return types.RunInfo{}, false
		}
		return types.RunInfo{Principal: p}, true
	}))
	return &consumer{
		runs:    runs,
		journal: stores.NewMemoryJournal(),
		audit:   stores.NewMemoryAuditLog(),
		kill:    kill,
		do:      do,
	}
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

// handle processes one delivered message. A first delivery runs the flow:
// approve, consult the kill flag, execute the refund once, journal it. A
// redelivery of the same message key returns the recorded result and never
// re-executes the effect.
func (c *consumer) handle(ctx context.Context, ev RefundEvent) (result string, err error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// The message key is the OperationID (ADR-0077): identical business
	// work carries an identical key across redeliveries.
	lease, err := c.runs.Start(ctx, stores.Run{
		SessionID:   ev.SessionID(),
		RunID:       ev.RunID(),
		Flow:        "refunds",
		Backend:     "kafka",
		OperationID: ev.Key,
	}, stores.LeaseTTL)
	if err != nil {
		if dup, ok := errors.AsType[stores.OperationExistsError](err); ok {
			run, opErr := c.runs.ByOperation(ctx, ev.Tenant(), ev.Key)
			if opErr != nil {
				return "", opErr
			}
			return run.ResultRef, ErrRedelivered{RunID: dup.RunID, Result: run.ResultRef}
		}
		return "", err
	}
	defer func() { _ = c.runs.Finish(ctx, lease, stores.Finished, nil, result) }()

	args, err := json.Marshal(map[string]any{"order_id": ev.OrderID, "amount_cents": ev.Cents})
	if err != nil {
		return "", err
	}
	key := types.CallKey{SessionID: ev.SessionID(), CallID: "refund"}
	fp := gohan.ToolFingerprint(types.ToolSpec{Name: "refund"}, args)

	// Inside the run, the journal fingerprints the call: a retried effect
	// replays the recorded result instead of charging the card again.
	entry, reserved, err := c.journal.Reserve(ctx, key, fp)
	if err != nil {
		return "", err
	}
	if !reserved {
		if entry.State == stores.Completed {
			return resultText(entry.Result), nil
		}
		return "", fmt.Errorf("refund call %v still reserved", key)
	}

	// Approval precedes the effect; the kill flag is consulted live, after
	// the approval, so an operator can stop refunds between the two.
	c.approve(ctx, ev)
	if c.kill() {
		c.decide(ctx, ev, "flag_denied")
		return "denied: refunds.kill is on", nil
	}
	c.decide(ctx, ev, "allowed")

	res, err := c.do(ev.OrderID, ev.Cents)
	if err != nil {
		return "", err
	}
	out := types.ToolResult{
		ID:      key.CallID,
		Content: []types.Block{types.Text{Text: res}},
		Outcome: types.Succeeded,
	}
	if err := c.journal.Complete(ctx, key, out); err != nil {
		return "", err
	}
	return res, nil
}

func (c *consumer) approve(ctx context.Context, ev RefundEvent) {
	_ = c.audit.Append(ctx, stores.AuditRecord{
		Kind:      stores.AuditApproval,
		SessionID: ev.SessionID(),
		RunID:     ev.RunID(),
		Flow:      "refunds",
		Tool:      "refund",
		Approver:  "ops-oncall",
		Verdict:   "approved",
	})
}

func (c *consumer) decide(ctx context.Context, ev RefundEvent, decision string) {
	_ = c.audit.Append(ctx, stores.AuditRecord{
		Kind:      stores.AuditToolDecision,
		SessionID: ev.SessionID(),
		RunID:     ev.RunID(),
		Flow:      "refunds",
		Tool:      "refund",
		Decision:  decision,
	})
}

func resultText(res types.ToolResult) string {
	for _, b := range res.Content {
		if t, ok := b.(types.Text); ok {
			return t.Text
		}
	}
	return ""
}

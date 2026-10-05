package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
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

// RunID names the bounded run one delivery drives.
func (ev RefundEvent) RunID() string { return "run-" + ev.Key }

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
func newOfflineDeliverer(kill func() bool) *deliverer {
	d := newDeliverer(nil)
	d.c = newConsumer(d.settle, kill)
	return d
}

// replay reads the journaled result for the event's refund call back from
// the journal, the way a retried effect replays it.
func (d *deliverer) replay(ev RefundEvent) (string, error) {
	args, err := json.Marshal(map[string]any{"order_id": ev.OrderID, "amount_cents": ev.Cents})
	if err != nil {
		return "", err
	}
	fp := gohan.ToolFingerprint(types.ToolSpec{Name: "refund"}, args)
	entries, err := d.c.journal.ByFingerprint(context.Background(), ev.SessionID(), fp)
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

// settle is the refund side effect the consumer journals.
func (d *deliverer) settle(orderID string, cents int) (string, error) {
	d.mu.Lock()
	d.refunded[orderID]++
	d.mu.Unlock()
	return fmt.Sprintf("refunded %d cents for %s", cents, orderID), nil
}

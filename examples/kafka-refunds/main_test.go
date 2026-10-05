package main

import (
	"context"
	"errors"
	"testing"

	"github.com/victorzhuk/gohan/core/stores"
)

func TestKafkaRefundsOffline(t *testing.T) {
	ev := RefundEvent{Key: "ord-42", OrderID: "ord-42", Cents: 2500}

	t.Run("engines.redelivery-returns-the-same-result", func(t *testing.T) {
		d, err := newOfflineDeliverer(func() bool { return false })
		if err != nil {
			t.Fatalf("deliverer: %v", err)
		}
		first, err := d.deliver(ev)
		if err != nil {
			t.Fatalf("first delivery: %v", err)
		}
		if n := d.executes(ev.OrderID); n != 1 {
			t.Fatalf("effect ran %d times on first delivery, want 1", n)
		}
		again, err := d.deliver(ev)
		redelivered, ok := asRedelivered(err)
		if !ok {
			t.Fatalf("redelivery error %v, want ErrRedelivered", err)
		}
		recorded, rerr := d.c.runs.ByOperation(context.Background(), ev.Tenant(), ev.Key)
		if rerr != nil {
			t.Fatalf("recorded run: %v", rerr)
		}
		if redelivered.RunID != recorded.RunID {
			t.Fatalf("redelivery run id %q, want the recorded %q", redelivered.RunID, recorded.RunID)
		}
		if again != first {
			t.Fatalf("redelivery result %q, want the recorded %q", again, first)
		}
		if n := d.executes(ev.OrderID); n != 1 {
			t.Fatalf("effect ran %d times after redeliveries, want 1", n)
		}
	})

	t.Run("engines.live-deny-beats-approval", func(t *testing.T) {
		killed := true
		d, err := newOfflineDeliverer(func() bool { return killed })
		if err != nil {
			t.Fatalf("deliverer: %v", err)
		}
		res, err := d.deliver(ev)
		if err != nil {
			t.Fatalf("delivery: %v", err)
		}
		if n := d.executes(ev.OrderID); n != 0 {
			t.Fatalf("effect executed %d times under the kill flag, want 0", n)
		}
		if res == "" {
			t.Fatal("denied delivery returned an empty result")
		}
		var decisions []string
		for rec, err := range d.c.audit.Read(context.Background(), ev.SessionID()) {
			if err != nil {
				t.Fatalf("audit read: %v", err)
			}
			switch rec.Kind {
			case stores.AuditApproval:
				decisions = append(decisions, rec.Verdict)
			case stores.AuditToolDecision:
				decisions = append(decisions, rec.Decision)
			}
		}
		if len(decisions) != 2 || decisions[0] != "approved" || decisions[1] != "flag_denied" {
			t.Fatalf("audit decisions %v, want [approved flag_denied]", decisions)
		}
		killed = false
		next := RefundEvent{Key: "ord-43", OrderID: "ord-43", Cents: 100}
		if _, err := d.deliver(next); err != nil {
			t.Fatalf("delivery after flag off: %v", err)
		}
		if n := d.executes(next.OrderID); n != 1 {
			t.Fatalf("effect executed %d times after the flag went off, want 1", n)
		}
	})

	t.Run("journal replay inside the run", func(t *testing.T) {
		d, err := newOfflineDeliverer(func() bool { return false })
		if err != nil {
			t.Fatalf("deliverer: %v", err)
		}
		if _, err := d.deliver(ev); err != nil {
			t.Fatalf("delivery: %v", err)
		}
		before := d.executes(ev.OrderID)
		replay, err := d.replay(ev)
		if err != nil {
			t.Fatalf("journal replay: %v", err)
		}
		if replay == "" {
			t.Fatal("journal replay returned an empty result")
		}
		if d.executes(ev.OrderID) != before {
			t.Fatal("journal replay executed the effect again")
		}
	})
}

func asRedelivered(err error) (ErrRedelivered, bool) {
	e, ok := errors.AsType[ErrRedelivered](err)
	return e, ok
}

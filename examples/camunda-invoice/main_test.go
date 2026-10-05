package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

func resetExecuted() { executed = nil }

func testJob() invoiceJob {
	return invoiceJob{
		OperationID: "INV-2026-0042",
		Invoice:     "INV-2026-0042",
		AmountCents: 149500,
	}
}

// startWorkerRun claims a lease for one worker on the job's operation.
// The session and run ids carry the worker name, so two workers race on
// the same OperationID and the store decides the winner.
func startWorkerRun(ctx context.Context, runs *stores.MemoryRuns, job invoiceJob, worker string) (stores.Lease, stores.Run, error) {
	run := stores.Run{
		SessionID:   "invoice-" + job.OperationID + "-" + worker,
		RunID:       "run-" + job.OperationID + "-" + worker,
		RootRunID:   "run-" + job.OperationID,
		OperationID: job.OperationID,
		Flow:        "camunda-invoice",
		Backend:     "scripted",
		StartedAt:   time.Now(),
		Heartbeat:   time.Now(),
	}
	lease, err := runs.Start(ctx, run, stores.LeaseTTL)
	return lease, run, err
}

func TestCamundaInvoiceOffline(t *testing.T) {
	ctx := context.Background()

	t.Run("engines.duplicate-workers", func(t *testing.T) {
		resetExecuted()
		clock := &fakeClock{t: time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)}
		f := newFlow(clock)
		job := testJob()
		ready := make(chan struct{})
		errs := make(chan struct {
			lease stores.Lease
			run   stores.Run
			err   error
		}, 2)
		var wg sync.WaitGroup
		for i := range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-ready
				lease, run, err := startWorkerRun(ctx, f.runs, job, fmt.Sprint(i))
				errs <- struct {
					lease stores.Lease
					run   stores.Run
					err   error
				}{lease, run, err}
			}()
		}
		close(ready)
		wg.Wait()
		close(errs)

		var wins, losses int
		for res := range errs {
			if res.err == nil {
				wins++
				continue
			}
			var exists stores.OperationExistsError
			if !errors.As(res.err, &exists) {
				t.Fatalf("loser error %v, want OperationExistsError", res.err)
			}
			losses++
		}
		if wins != 1 || losses != 1 {
			t.Fatalf("got %d wins, %d losses; want exactly one of each", wins, losses)
		}
		// The winner claims the job on a fresh operation: the race above
		// decided the worker, the governed path drives the work item.
		winner := testJob()
		winner.OperationID += "-winner"
		out, err := f.Process(ctx, winner)
		if err != nil {
			t.Fatalf("winner process: %v", err)
		}
		if !out.executed {
			t.Fatalf("winner outcome %+v, want the invoice sent once", out)
		}
		if got := len(executed); got != 1 {
			t.Fatalf("executed %d tools, want exactly 1", got)
		}
	})

	t.Run("engines.suspended-runs-are-not-reclaimed", func(t *testing.T) {
		resetExecuted()
		clock := &fakeClock{t: time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)}
		f := newFlow(clock)
		job := testJob()
		suspended, err := f.suspendForApproval(ctx, job)
		if err != nil {
			t.Fatalf("user task suspend: %v", err)
		}
		if !suspended {
			t.Fatal("user task did not suspend the run")
		}
		clock.Advance(3 * 24 * time.Hour)

		stale, err := f.runs.Stale(ctx, 72*time.Hour, 10)
		if err != nil {
			t.Fatalf("stale: %v", err)
		}
		if len(stale) != 0 {
			t.Fatalf("stale listed %v, want the suspended run left alone", stale)
		}
		run, err := f.runs.ByOperation(ctx, f.origin.Tenant, job.OperationID)
		if err != nil {
			t.Fatalf("by operation: %v", err)
		}
		if _, err := f.runs.Reclaim(ctx, run, stores.LeaseTTL); !errors.Is(err, types.ErrRunNotActive) {
			t.Fatalf("reclaim error %v, want ErrRunNotActive", err)
		}
		got, err := f.runs.ByOperation(ctx, f.origin.Tenant, job.OperationID)
		if err != nil {
			t.Fatalf("by operation: %v", err)
		}
		if got.State != stores.Suspended {
			t.Fatalf("run state %v, want Suspended", got.State)
		}
	})

	t.Run("engines.revoked-authority-on-resume", func(t *testing.T) {
		resetExecuted()
		clock := &fakeClock{t: time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)}
		f := newFlow(clock)
		f.resumeScopes = []string{approvalScope}
		out, err := f.Process(ctx, testJob())
		if err != nil {
			t.Fatalf("process: %v", err)
		}
		if !out.denied || out.executed {
			t.Fatalf("outcome %+v, want the pending call denied on resume", out)
		}
		if out.reason != "originator scope revoked while suspended" {
			t.Fatalf("reason %q, want the revocation verdict", out.reason)
		}
		if len(executed) != 0 {
			t.Fatalf("executed %v, want nothing run", executed)
		}
	})

	t.Run("engines.stale-control-state", func(t *testing.T) {
		resetExecuted()
		clock := &fakeClock{t: time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)}
		f := newFlow(clock)
		f.outageOnResume = true
		out, err := f.Process(ctx, testJob())
		if err != nil {
			t.Fatalf("process: %v", err)
		}
		if !out.suspended || out.denied {
			t.Fatalf("outcome %+v, want AwaitingControl while the outage is fresh", out)
		}

		clock.Advance(MaxControlWait + time.Minute)
		out, err = f.Resume(ctx, testJob())
		if err != nil {
			t.Fatalf("resume past the wait bound: %v", err)
		}
		if !out.denied || out.suspended {
			t.Fatalf("outcome %+v, want the control-wait denial", out)
		}
		if out.reason == "" {
			t.Fatal("denial carries no reason")
		}
		if len(executed) != 0 {
			t.Fatalf("executed %v, want nothing run", executed)
		}
	})

	t.Run("live deny beats approval", func(t *testing.T) {
		resetExecuted()
		clock := &fakeClock{t: time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)}
		f := newFlow(clock)
		f.killOnResume = true
		out, err := f.Process(ctx, testJob())
		if err != nil {
			t.Fatalf("process: %v", err)
		}
		if !out.denied || out.executed {
			t.Fatalf("outcome %+v, want the approved call denied by the live flag", out)
		}
		want := []string{"approved", "flag_denied"}
		if !slices.Equal(f.audit, want) {
			t.Fatalf("audit %v, want %v", f.audit, want)
		}
		if len(executed) != 0 {
			t.Fatalf("executed %v, want the tool never run", executed)
		}
	})
}

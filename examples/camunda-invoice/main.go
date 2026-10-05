// Command camunda-invoice walks one offline invoice job through the
// composition an external workflow server would otherwise drive: fetch the
// work item, claim a run, ask the user task for approval, and re-check
// live control state on resume. Memory stores and a fake clock keep it
// offline: no network, no keys.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// approvalToken marks the user task's pending request.
const approvalToken = types.ResumeToken("approval:invoice.send")

// flow drives one job from fetch to verdict. The test fields shape the
// world the resume path wakes up into.
type flow struct {
	runs    *stores.MemoryRuns
	control *controlPlane
	clock   *fakeClock
	origin  types.Principal

	killOnResume   bool
	outageOnResume bool
	resumeScopes   []string
	audit          []string
}

// fakeClock adapts the testkit clock to the store's clock option.
type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time          { return c.t }
func (c *fakeClock) Advance(d time.Duration) { c.t = c.t.Add(d) }

func newFlow(clock *fakeClock) *flow {
	return &flow{
		runs:    stores.NewMemoryRuns(stores.WithMemoryRunClock(clock.Now)),
		control: newControlPlane(clock.Now()),
		clock:   clock,
		origin:  approverPrincipal("clerk", sendScope, approvalScope),
	}
}

// Process composes one job offline: start the run, run the user task
// (the run suspends for approval), grant the approval, and resume. The
// resume path owns the final verdict.
func (f *flow) Process(ctx context.Context, job invoiceJob) (outcome, error) {
	lease, run, err := startRun(ctx, f.runs, job)
	if err != nil {
		return outcome{}, fmt.Errorf("start run: %w", err)
	}
	return f.Claimed(ctx, job, lease, run)
}

// Claimed runs the user task on a claimed lease and then the resume path.
func (f *flow) Claimed(ctx context.Context, job invoiceJob, lease stores.Lease, run stores.Run) (outcome, error) {
	if err := askApproval(ctx, f.runs, lease, approvalToken); err != nil {
		return outcome{}, fmt.Errorf("user task suspend: %w", err)
	}
	f.audit = append(f.audit, "approved")
	return f.Resume(ctx, job, run)
}

// Resume runs the post-approval half: the live re-checks and the verdict.
func (f *flow) Resume(ctx context.Context, job invoiceJob, run stores.Run) (outcome, error) {
	suspendedAt := f.clock.Now()

	f.control.SetReachable(!f.outageOnResume)
	f.control.SetKill(f.killOnResume)
	if f.resumeScopes == nil {
		f.resumeScopes = f.origin.Scopes
	}
	resumed := f.origin
	resumed.Scopes = f.resumeScopes

	inv := invoiceInvocation(job)
	inv.Run = runInfo(run.RunID, resumed)

	kind, reason := resume(ctx, f.control, inv, suspendedAt, f.clock.Now())
	out := outcome{reason: types.SuspendReason(reason)}
	switch kind {
	case resumeSuspendControl:
		out.suspended = true
	case resumeDenyControlWait, resumeDenyKill, resumeDenyAuthority:
		out.denied = true
		if kind == resumeDenyKill {
			f.audit = append(f.audit, "flag_denied")
		}
	case resumeAllow:
		live, err := f.runs.Resuming(ctx, run.RunID, stores.LeaseTTL)
		if err != nil {
			return out, fmt.Errorf("resume run: %w", err)
		}
		recordExecution(inv.Spec.Name)
		out.executed = true
		if err := f.runs.Finish(ctx, live, stores.Finished, nil, ""); err != nil {
			return out, fmt.Errorf("finish run: %w", err)
		}
	}
	return out, nil
}

// duplicateWorker attempts to claim the same operation from a second
// worker. The store decides the winner.
func duplicateWorker(ctx context.Context, f *flow, job invoiceJob, worker string) (stores.Lease, stores.Run, error) {
	return startWorkerRun(ctx, f.runs, job, worker)
}

// Run processes the first queued job end to end; main prints the verdict.
func Run(ctx context.Context) (outcome, error) {
	clock := &fakeClock{t: time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)}
	f := newFlow(clock)
	queue := newJobQueue(invoiceJob{
		OperationID: "INV-2026-0042",
		Invoice:     "INV-2026-0042",
		AmountCents: 149500,
	})
	job, ok := queue.Fetch()
	if !ok {
		return outcome{}, errors.New("queue empty")
	}
	return f.Process(ctx, job)
}

func main() {
	out, err := Run(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, "camunda-invoice:", err)
		os.Exit(1)
	}
	switch {
	case out.executed:
		fmt.Println("invoice.send executed")
	case out.denied:
		fmt.Println("invoice.send denied:", out.reason)
	case out.suspended:
		fmt.Println("run suspended:", out.reason)
	}
}

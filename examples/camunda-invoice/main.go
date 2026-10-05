// Command camunda-invoice walks one offline invoice job through the
// composition an external workflow server would otherwise drive: fetch the
// work item, claim a governed run, let the user task suspend it for
// approval, and re-check live control state on resume through the flow's
// own decider. Memory stores and a fake clock keep it offline: no network,
// no keys.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// flow drives one job from fetch to verdict. The test fields shape the
// world the resume path wakes up into.
type flow struct {
	runs        *stores.MemoryRuns
	checkpoints *controlWaitCheckpoints
	control     *controlPlane
	clock       *fakeClock
	origin      types.Principal
	tool        *invoiceTool
	sessions    *stores.MemorySessionLog
	model       *scriptedModel

	killOnResume   bool
	outageOnResume bool
	resumeScopes   []string
	audit          []string

	approved    bool
	suspendedAt time.Time
	token       gohan.ResumeToken
	denial      string
	conv        gohan.Conversation
}

// deciderFunc adapts the flow's policy function to the decider seam.
type deciderFunc func(context.Context, *permission.ToolInvocation) (types.Decision[permission.Verdict], error)

func (d deciderFunc) Decide(ctx context.Context, inv *permission.ToolInvocation) (types.Decision[permission.Verdict], error) {
	return d(ctx, inv)
}

// fakeClock adapts the testkit clock to the store's clock option.
type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time          { return c.t }
func (c *fakeClock) Advance(d time.Duration) { c.t = c.t.Add(d) }

func newFlow(clock *fakeClock) *flow {
	f := &flow{
		runs: stores.NewMemoryRuns(
			stores.WithMemoryRunClock(clock.Now),
			stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
				p, ok := types.PrincipalFrom(ctx)
				return types.RunInfo{Principal: p}, ok
			}),
		),
		control:  newControlPlane(clock.Now()),
		clock:    clock,
		origin:   approverPrincipal("clerk", sendScope, approvalScope),
		tool:     &invoiceTool{},
		sessions: stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)),
		model:    &scriptedModel{},
		checkpoints: &controlWaitCheckpoints{
			MemoryCheckpoints: stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointClock(clock.Now), stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom)),
			now:               clock.Now,
		},
	}
	return f
}

// controlWaitCheckpoints keeps a suspension token alive past the flow's
// MaxControlWait bound: a stale resume must be rejected by the flow's own
// control-state rule, not by the store's token expiry, which otherwise
// lapses with the suspension lease long before that bound.
type controlWaitCheckpoints struct {
	*stores.MemoryCheckpoints
	now func() time.Time
}

func (s *controlWaitCheckpoints) Put(ctx context.Context, cp stores.Checkpoint) (types.ResumeToken, error) {
	cp.ExpiresAt = s.now().Add(MaxControlWait + time.Hour)
	return s.MemoryCheckpoints.Put(ctx, cp)
}

// conversationFor builds the governed native conversation for the flow
// once and reuses it: a resume token is bound to the conversation that
// minted it, so suspension and resume must drive the same instance. The
// scripted definition is registered at build time with the flow's
// decider, so every side effect is gated before it executes and nothing
// here persists history or results by hand.
func (f *flow) conversationFor() (gohan.Conversation, error) {
	if f.conv != nil {
		return f.conv, nil
	}
	stack, err := gohan.Build(
		gohan.WithStores(stores.Stores{
			SessionLog:  f.sessions,
			Runs:        f.runs,
			Checkpoints: f.checkpoints,
		}),
		gohan.WithModels(f.model),
		gohan.WithNativeAgent(gohan.NativeSpec{
			Request: gohan.FlowRequest{Name: "camunda-invoice"},
			Profile: "scripted",
			Tools:   []types.Tool{f.tool},
			Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
				msgs := append([]types.Message{}, in.History...)
				return types.ModelRequest{Messages: append(msgs, in.Input...)}, nil
			},
			Decider: deciderFunc(f.decide),
		}),
	)
	if err != nil {
		return nil, err
	}
	conv, err := gohan.NewNativeConversation(stack, "camunda-invoice",
		gohan.WithConversationRuns(f.runs),
		gohan.WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(f.clock.Now))),
		gohan.WithConversationApprovalPolicy(approvalPolicy{}),
	)
	if err != nil {
		return nil, err
	}
	f.conv = conv
	return conv, nil
}

// decide is the flow's tool policy and the user task in one: before the
// approval arrives every side effect asks (the run suspends for the
// human); after it, the live re-checks run in order — control wait, the
// flags provider outage, the originator's current scopes, then the live
// kill flag. A live denial wins even after an approval arrived.
func (f *flow) decide(_ context.Context, inv *permission.ToolInvocation) (types.Decision[permission.Verdict], error) {
	if !f.approved {
		return types.Decision[permission.Verdict]{}, nil
	}
	if f.clock.Now().Sub(f.suspendedAt) > MaxControlWait {
		f.denial = "control state unresolved past MaxControlWait"
		return types.Decision[permission.Verdict]{Value: permission.DenyVerdict, Confidence: 1}, nil
	}
	if _, _, err := f.control.KillOn(); err != nil {
		// Control state unreadable: the call cannot be cleared, so it
		// asks again and the run suspends until control returns.
		return types.Decision[permission.Verdict]{Value: permission.Ask, Confidence: 1}, nil
	}
	if !f.authorised(inv) {
		f.denial = "originator scope revoked while suspended"
		return types.Decision[permission.Verdict]{Value: permission.DenyVerdict, Confidence: 1}, nil
	}
	if kill, _, _ := f.control.KillOn(); kill {
		f.denial = "invoice send denied by the live kill flag"
		return types.Decision[permission.Verdict]{Value: permission.DenyVerdict, Confidence: 1}, nil
	}
	return types.Decision[permission.Verdict]{Value: permission.Allow, Confidence: 1}, nil
}

// authorised compares the originator's current scopes against the pending
// call's requirements, so a revocation while suspended denies on resume.
func (f *flow) authorised(inv *permission.ToolInvocation) bool {
	scopes := f.origin.Scopes
	if f.resumeScopes != nil {
		scopes = f.resumeScopes
	}
	for _, want := range inv.Spec.RequiredScopes {
		found := false
		for _, have := range scopes {
			if have == want {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// suspendForApproval runs the user task: the governed send suspends the
// run on the pending side effect and the token rides the suspension.
func (f *flow) suspendForApproval(ctx context.Context, job invoiceJob) (bool, error) {
	conv, err := f.conversationFor()
	if err != nil {
		return false, err
	}
	ctx = types.WithPrincipal(ctx, f.origin)
	// The gate reads the run identity from the driver's context, and the
	// idempotency key names the run's operation.
	ctx = types.WithRunInfo(ctx, types.RunInfo{Flow: "camunda-invoice", Principal: f.origin})
	ctx = types.WithIdempotencyKey(ctx, job.OperationID)
	var suspended bool
	for ev, err := range conv.Send(ctx, "invoice-"+job.OperationID, userTurn(job)) {
		if err != nil {
			return suspended, err
		}
		if s, ok := ev.(types.Suspended); ok {
			suspended, f.token, f.suspendedAt = true, s.Token, f.clock.Now()
		}
	}
	return suspended, nil
}

// Process composes one job offline: start the governed run, let the user
// task suspend it for approval, grant the approval, and resume. The
// resume path owns the final verdict.
func (f *flow) Process(ctx context.Context, job invoiceJob) (outcome, error) {
	out := outcome{reason: types.SuspendReason("approval required")}
	suspended, err := f.suspendForApproval(ctx, job)
	if err != nil {
		return out, err
	}
	out.suspended = suspended
	if !suspended {
		out.executed = len(f.tool.ran()) > 0
		return out, nil
	}
	f.audit = append(f.audit, "approved")
	return f.Resume(ctx, job)
}

// Resume grants the pending approval and drives the post-approval half:
// the decider's live re-checks and the verdict.
func (f *flow) Resume(ctx context.Context, job invoiceJob) (outcome, error) {
	conv, err := f.conversationFor()
	if err != nil {
		return outcome{}, err
	}
	var out outcome
	token := f.token
	if token == "" {
		return out, errors.New("no suspension token to resume")
	}
	f.control.SetReachable(!f.outageOnResume)
	f.control.SetKill(f.killOnResume)
	// The engine holds the approval while live control state is
	// unreadable: an approved call must not execute blind. Once the
	// control wait bound passes, the approval goes through and the
	// decider owns the denial.
	if _, _, err := f.control.KillOn(); err != nil && f.clock.Now().Sub(f.suspendedAt) <= MaxControlWait {
		return outcome{suspended: true, reason: "awaiting control state"}, nil
	}
	f.approved = true
	f.denial = ""
	if f.resumeScopes == nil {
		f.resumeScopes = f.origin.Scopes
	}
	f.token = ""
	// The ask suspends with reason HumanApproval, and the approval rides
	// the decision, not a delivery payload: the live re-checks still own
	// the verdict on replay.
	for ev, err := range conv.Resume(types.WithPrincipal(ctx, f.origin), token, gohan.Approve()) {
		if err != nil {
			return out, err
		}
		if s, ok := ev.(types.Suspended); ok {
			out.suspended = true
			out.reason = s.Reason
			f.token, f.suspendedAt = s.Token, f.clock.Now()
		}
	}
	if f.denial != "" {
		out.denied = true
		out.reason = types.SuspendReason(f.denial)
		if f.killOnResume {
			f.audit = append(f.audit, "flag_denied")
		}
	}
	out.executed = len(f.tool.ran()) > 0
	return out, nil
}

func userTurn(job invoiceJob) types.Message {
	return types.Message{
		Role:   types.RoleUser,
		Blocks: []types.Block{types.Text{Text: "send invoice " + job.Invoice}},
	}
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

// Command temporal-travel walks a durable travel booking through one
// activity, one approval signal, a crash after the resume input was
// consumed, and a recovery through Stack.Recover. The run is driven by
// gohan's governed native conversation over memory stores: the
// conversation persists every append, the checkpoint store mints the
// single-use resume token, and recovery replays from the persisted
// state. It runs offline: no network, no API keys, no workflow engine —
// the fixture in fixtures.go drives the signal and the crash.
package main

import (
	"context"
	"encoding/json/jsontext"
	"fmt"
	"iter"
	"os"
	"slices"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// flowName is the session and flow the booking runs under.
const flowName = "temporal-travel"

// LiveFlags is the rollout flag provider. The scripted model reads it
// once at the first turn: the value it reads lands in the booking
// arguments the history captures, and replay sees the captured
// arguments even after the provider changes.
type LiveFlags struct {
	vals map[string]any
}

func NewLiveFlags(vals map[string]any) *LiveFlags {
	copied := make(map[string]any, len(vals))
	for k, v := range vals {
		copied[k] = v
	}
	return &LiveFlags{vals: copied}
}

func (f *LiveFlags) Set(key string, v any) {
	f.vals[key] = v
}

func (f *LiveFlags) Snapshot() map[string]any {
	out := make(map[string]any, len(f.vals))
	for k, v := range f.vals {
		out[k] = v
	}
	return out
}

// travelTools carries what one booking run did: the executed step
// marks, the activity counters the fixtures increment, one entry per
// step the run recorded.
type travelTools struct {
	steps    []string
	searches int
	bookings int
}

func (tt *travelTools) mark(step string) {
	tt.steps = append(tt.steps, step)
}

// Trip is one booking run over memory stores.
type Trip struct {
	rt       *travelTools
	flags    *LiveFlags
	sessions *stores.MemorySessionLog
	runs     *stores.MemoryRuns
	cps      *stores.MemoryCheckpoints
	journal  *stores.MemoryJournal
	events   *stores.MemoryEventLog
	stack    *gohan.Stack
	conv     gohan.Conversation
	token    types.ResumeToken
}

// NewTrip assembles the offline stores — session log, runs, checkpoints,
// journal, event log — and wires the governed stack and conversation
// over them.
func NewTrip(flags *LiveFlags) *Trip {
	t := &Trip{
		rt:       &travelTools{},
		flags:    flags,
		sessions: stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)),
		runs:     stores.NewMemoryRuns(),
		cps:      stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom)),
		journal:  stores.NewMemoryJournal(),
		events:   stores.NewMemoryEventLog(),
	}
	if err := t.start(); err != nil {
		panic("temporal-travel: " + err.Error())
	}
	return t
}

// bookingApproval grants the booking ask to the traveler: one approval
// from the session's owner settles it, the run's own tool policy still
// gates the execution on replay.
type bookingApproval struct{}

func (bookingApproval) ApprovalPolicy(context.Context, types.RiskTier, string, bool) (permission.ApprovalPolicy, error) {
	return permission.ApprovalPolicy{Quorum: 1}, nil
}

// start wires a fresh governed stack and conversation over the trip's
// stores. Everything start builds lives in process memory only: a crash
// drops it, and the next start rebuilds it from the same stores.
func (t *Trip) start() error {
	stack, err := gohan.Build(
		gohan.WithStores(stores.Stores{
			SessionLog:  t.sessions,
			Runs:        t.runs,
			Checkpoints: t.cps,
			Journal:     t.journal,
		}),
		gohan.WithModels(&travelModel{trip: t}),
		gohan.WithCredentialSource(offlineCredentials{}),
		gohan.WithRecoveryRuntime(flowName, runtime.NewNative()),
		gohan.WithNativeAgent(gohan.NativeSpec{
			Request:   gohan.FlowRequest{Name: flowName},
			Profile:   "scripted",
			Assemble:  assemble,
			Tools:     t.tools(),
			ToolChain: t.toolChain(),
			Decider: policyDecider{
				"search_flights": {Value: permission.Allow, Confidence: 1},
				"book_flight":    {Value: permission.Ask, Confidence: 1},
			},
		}),
	)
	if err != nil {
		return err
	}
	conv, err := gohan.NewNativeConversation(stack, flowName,
		gohan.WithConversationRuns(t.runs),
		gohan.WithConversationEventLog(t.events),
		gohan.WithConversationApprovalPolicy(bookingApproval{}),
	)
	if err != nil {
		return err
	}
	t.stack, t.conv = stack, conv
	return nil
}

func assemble(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
	return types.ModelRequest{Messages: slices.Clone(in.History)}, nil
}

// travelModel scripts the model from the persisted history alone: no
// tool result in the history means the first turn asks for the search
// and the booking, any result means the closing reply. A crash drops
// the model with the rest of the process; the history replays its
// turns without it.
type travelModel struct {
	trip *Trip
}

func (m *travelModel) Profile() types.ModelProfile {
	return types.ModelProfile{Name: "scripted", Caps: types.Caps{Tools: true}}
}

func (m *travelModel) Generate(_ context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	results := 0
	for _, msg := range req.Messages {
		for _, b := range msg.Blocks {
			if _, ok := b.(types.ToolResult); ok {
				results++
			}
		}
	}
	return func(yield func(types.ModelChunk, error) bool) {
		m.trip.rt.mark("model-turn")
		if results > 0 {
			yield(types.ModelChunk{Kind: types.DeltaText, Delta: "Flight AF123 Paris->Tokyo booked."}, nil)
			yield(types.ModelChunk{Finish: types.FinishStop}, nil)
			return
		}
		// The flag value is frozen here: it travels inside the
		// booking arguments the history captures, never through the
		// live provider.
		online, _ := m.trip.flags.Snapshot()["booking.online"].(bool)
		search := types.ToolUse{ID: "call_search_flights", Name: "search_flights",
			Args: jsontext.Value(`{"from":"paris","to":"tokyo"}`)}
		book := types.ToolUse{ID: "call_book_flight", Name: "book_flight",
			Args: jsontext.Value(fmt.Sprintf(`{"route":"paris-tokyo","online":%v}`, online))}
		yield(types.ModelChunk{ToolUse: &search}, nil)
		yield(types.ModelChunk{ToolUse: &book}, nil)
		yield(types.ModelChunk{Finish: types.FinishToolUse}, nil)
	}
}

// Run drives the booking until the approval signal is awaited, and
// keeps the single-use resume token the signal consumes.
func (t *Trip) Run(ctx context.Context) error {
	return t.drive(ctx, t.conv.Send(t.ctx(ctx), flowName, types.Message{
		Role:   types.RoleUser,
		Blocks: []types.Block{types.Text{Text: "Book the Paris->Tokyo flight."}},
	}))
}

// Signal records the traveler's approval decision: it consumes the
// token with Approve and drives the run past the ask, exactly what an
// engine's signal handler records. The decision is the client's; a
// crash after it leaves recovery to replay the settled call.
func (t *Trip) Signal(ctx context.Context) error {
	for ev, err := range t.conv.Resume(t.ctx(ctx), t.token, gohan.Approve()) {
		if err != nil {
			return err
		}
		if s, ok := ev.(types.Suspended); ok {
			t.token = s.Token
		}
	}
	return nil
}

// RecoverReplay recovers the run after a crash: Stack.Recover finds the
// run whose checkpoint token was consumed while it stayed Suspended,
// re-drives it from the stores, and the booking executes exactly once.
func (t *Trip) RecoverReplay(ctx context.Context) error {
	return t.stack.Recover(t.ctx(ctx), 8)
}

// ReplayStep re-enters the suspended run from fresh live wiring: a new
// conversation over the same stores resumes the ask, the journal answers
// the already-completed search, and the booking step re-executes exactly
// once. It reports whether the replay advanced the run without repeating
// the completed activity.
func (t *Trip) ReplayStep(ctx context.Context) (bool, error) {
	probe := &Trip{
		rt:       t.rt,
		flags:    t.flags,
		sessions: t.sessions,
		runs:     t.runs,
		cps:      t.cps,
		journal:  t.journal,
		events:   t.events,
	}
	if err := probe.start(); err != nil {
		return false, err
	}
	searches := t.rt.searches
	bookings := t.rt.bookings
	done := false
	for ev, err := range probe.conv.Resume(probe.ctx(ctx), t.token, gohan.Approve()) {
		if err != nil {
			return false, err
		}
		if _, ok := ev.(types.Done); ok {
			done = true
		}
	}
	return done && t.rt.bookings == bookings+1 && t.rt.searches == searches, nil
}

func (t *Trip) drive(ctx context.Context, seq iter.Seq2[types.Event, error]) error {
	for ev, err := range seq {
		if err != nil {
			return err
		}
		if s, ok := ev.(types.Suspended); ok {
			if t.token == "" {
				t.token = s.Token
			}
			t.rt.mark("suspended")
		}
	}
	return nil
}

func (t *Trip) ctx(ctx context.Context) context.Context {
	return types.WithPrincipal(ctx, types.Principal{
		Tenant: "local", Subject: "traveler",
		Scopes: []string{types.ScopeSessionRead, types.ScopeSessionWrite},
	})
}

func main() {
	if err := runBooking(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "temporal-travel:", err)
		os.Exit(1)
	}
	fmt.Println("recovered: the search replayed from the journal, the booking executed exactly once")
}

// runBooking is the whole offline story: run to the suspension, deliver
// the signal, crash after the consume, recover.
func runBooking(ctx context.Context) error {
	trip := NewTrip(NewLiveFlags(map[string]any{"booking.online": true}))
	if err := trip.Run(ctx); err != nil {
		return err
	}
	if err := trip.Signal(ctx); err != nil {
		return err
	}
	trip.crash()
	return trip.RecoverReplay(ctx)
}

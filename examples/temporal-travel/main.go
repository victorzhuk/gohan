// Command temporal-travel walks a durable travel booking through one
// activity, one approval signal, a crash after the resume input was
// consumed, and a replay from the journal. It runs offline: no network,
// no API keys, no workflow engine — the fixture in fixtures.go drives
// the signal and the crash.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// LiveFlags is the rollout flag provider. A run reads it once at start:
// the snapshot lands in runtime.State.Flags, and replay sees the pinned
// values even after the provider changes.
type LiveFlags struct {
	mu   sync.Mutex
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
	f.mu.Lock()
	defer f.mu.Unlock()
	f.vals[key] = v
}

func (f *LiveFlags) Snapshot() runtime.FlagSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(runtime.FlagSnapshot, len(f.vals))
	for k, v := range f.vals {
		out[k] = v
	}
	return out
}

// travelRuntime is the stepper for the booking flow. Each Step is one
// effect: search the flights, wait for the approval signal, book, done.
// Effect results are journaled, so a replay re-executes Step over the
// recorded results instead of re-running the activities.
type travelRuntime struct {
	sessionID string
	journal   *stores.MemoryJournal
	flags     *LiveFlags
	save      func(context.Context, stores.Checkpoint) (types.ResumeToken, error)

	// steps records the State after every executed Step, so a replay
	// can be compared against the first execution up to the
	// suspension point.
	steps     []runtime.State
	searches  int
	bookings  int
	lastToken types.ResumeToken
}

func (rt *travelRuntime) Name() string                         { return "temporal-travel.scripted" }
func (rt *travelRuntime) Granularity() runtime.StepGranularity { return runtime.GranularityEffect }

func (rt *travelRuntime) Start(_ context.Context, r runtime.AgentRun) (runtime.State, error) {
	return runtime.State{HistoryVersion: r.History.Version, Flags: rt.flags.Snapshot()}, nil
}

func (rt *travelRuntime) Step(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	switch st.Turn {
	case 0:
		if err := rt.searchFlights(ctx); err != nil {
			return st, nil, runtime.Continue, err
		}
		st.Turn, st.HistoryVersion = 1, st.HistoryVersion+1
		rt.steps = append(rt.steps, st)
		return st, nil, runtime.Continue, nil
	case 1:
		// The signal has not arrived: suspend behind a single-use
		// token. The flags snapshot travels inside the checkpoint
		// payload with the rest of the state.
		data, err := json.Marshal(st)
		if err != nil {
			return st, nil, runtime.Continue, err
		}
		p, _ := types.PrincipalFrom(ctx)
		tok, err := rt.save(ctx, stores.Checkpoint{
			SchemaVersion: stores.CurrentSchemaVersion,
			SessionID:     rt.sessionID,
			Flow:          "travel-booking",
			Backend:       rt.Name(),
			Reason:        types.HumanApproval,
			Originator:    p,
			Data:          data,
			ExpiresAt:     time.Now().Add(time.Hour),
		})
		if err != nil {
			return st, nil, runtime.Continue, err
		}
		rt.lastToken = tok
		rt.steps = append(rt.steps, st)
		return st, nil, runtime.SuspendedStatus, nil
	case 2:
		// The approval arrived. The booking reads the pinned flag,
		// never the live provider.
		if online, _ := st.Flags["booking.online"].(bool); !online {
			return st, nil, runtime.Continue, fmt.Errorf("travel: booking channel closed")
		}
		rt.bookings++
		st.Turn, st.HistoryVersion = 3, st.HistoryVersion+1
		rt.steps = append(rt.steps, st)
		return st, nil, runtime.Continue, nil
	default:
		return st, nil, runtime.DoneStatus, nil
	}
}

// searchFlights journals one activity call. A Completed entry is
// replayed as-is; only a miss executes the search.
func (rt *travelRuntime) searchFlights(ctx context.Context) error {
	key := types.CallKey{SessionID: rt.sessionID, CallID: "search-flights"}
	entry, _, err := rt.journal.Reserve(ctx, key, stores.Fingerprint("search-flights:"+rt.sessionID))
	if err != nil {
		return err
	}
	if entry.State == stores.Completed {
		return nil
	}
	rt.searches++
	return rt.journal.Complete(ctx, key, types.ToolResult{
		ID:      "search-flights",
		Content: []types.Block{types.Text{Text: "AF123 Paris->Tokyo"}},
		Outcome: types.Succeeded,
	})
}

// Trip is one booking run over memory stores.
type Trip struct {
	rt        *travelRuntime
	cps       *stores.MemoryCheckpoints
	journal   *stores.MemoryJournal
	flags     *LiveFlags
	runID     string
	token     types.ResumeToken
	recovered bool
}

// NewTrip assembles the offline stack: memory checkpoints, memory
// journal, and a live flag provider.
func NewTrip(flags *LiveFlags) *Trip {
	journal := stores.NewMemoryJournal()
	rt := &travelRuntime{sessionID: "trip", journal: journal, flags: flags}
	trip := &Trip{
		cps:     stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom)),
		journal: journal,
		flags:   flags,
		rt:      rt,
		runID:   "trip-run-1",
	}
	rt.save = trip.cps.Put
	return trip
}

// Run drives the flow until the approval signal is awaited, and returns
// the single-use resume token the signal consumes.
func (t *Trip) Run(ctx context.Context) error {
	ctx = types.WithPrincipal(ctx, types.Principal{
		Tenant: "local", Subject: "traveler",
		Scopes: []string{types.ScopeSessionRead, types.ScopeSessionWrite},
	})
	ctx = types.WithRunInfo(ctx, types.RunInfo{
		Flow: "travel-booking", SessionID: t.rt.sessionID,
		RunID: t.runID, RootRunID: t.runID,
	})
	st, err := t.rt.Start(ctx, runtime.AgentRun{})
	if err != nil {
		return err
	}
	t.token, err = t.drive(ctx, st)
	return err
}

// Signal delivers the approval signal: it consumes the token with an
// approval decision, exactly what an engine's signal handler calls.
func (t *Trip) Signal(ctx context.Context) (stores.Checkpoint, error) {
	return t.cps.Consume(ctx, t.token, stores.ResumeInput{Verdict: stores.VerdictApprove})
}

// RecoverReplay recovers the run after a crash: it reads the consumed
// input back through PendingInput and re-drives the checkpointed state.
// The token is single-use, so the input is applied exactly once: the
// run is no longer Resuming after the first recovery.
func (t *Trip) RecoverReplay(ctx context.Context) error {
	if t.recovered {
		return nil
	}
	cp, input, err := t.cps.PendingInput(ctx, t.runID)
	if err != nil {
		return err
	}
	if input.Verdict != stores.VerdictApprove {
		return fmt.Errorf("travel: consumed input is %v, want approval", input.Verdict)
	}
	var st runtime.State
	if err := json.Unmarshal(cp.Data, &st); err != nil {
		return err
	}
	// The consumed input is the approval: the resumed run starts past
	// the wait, the same way applyResume appends the decision before
	// the re-drive.
	st.Turn = 2
	_, err = t.drive(ctx, st)
	if err == nil {
		t.recovered = true
	}
	return err
}

// ReplayStep re-executes Step from the start state over the journal's
// recorded results on a fresh runtime, and reports whether the State
// sequence up to the suspension point matches the first execution.
func (t *Trip) ReplayStep(ctx context.Context) (bool, error) {
	probe := &travelRuntime{
		sessionID: t.rt.sessionID,
		journal:   t.rt.journal,
		flags:     t.rt.flags,
		save:      func(context.Context, stores.Checkpoint) (types.ResumeToken, error) { return "", nil },
	}
	start, err := probe.Start(ctx, runtime.AgentRun{})
	if err != nil {
		return false, err
	}
	st := start
	// Replay covers the steps up to the suspension point: the
	// booking itself only re-executes on a resumed run.
	for _, want := range t.rt.steps {
		next, _, status, err := probe.Step(ctx, st)
		if err != nil {
			return false, err
		}
		if !statesMatch(next, want) {
			return false, nil
		}
		if status != runtime.Continue {
			if status != runtime.SuspendedStatus {
				return false, fmt.Errorf("travel: replay status %v", status)
			}
			break
		}
		st = next
	}
	return true, nil
}

func statesMatch(a, b runtime.State) bool {
	if a.Turn != b.Turn || a.HistoryVersion != b.HistoryVersion {
		return false
	}
	if len(a.Flags) != len(b.Flags) {
		return false
	}
	for k, v := range a.Flags {
		if b.Flags[k] != v {
			return false
		}
	}
	return true
}

func (t *Trip) drive(ctx context.Context, st runtime.State) (types.ResumeToken, error) {
	for {
		next, _, status, err := t.rt.Step(ctx, st)
		if err != nil {
			return "", err
		}
		st = next
		if status == runtime.DoneStatus {
			return "", nil
		}
		if status == runtime.SuspendedStatus {
			return t.rt.lastToken, nil
		}
	}
}

func main() {
	if err := runBooking(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "temporal-travel:", err)
		os.Exit(1)
	}
}

// runBooking is the whole offline story: run to the suspension, deliver
// the signal, crash after the consume, recover, replay.
func runBooking(ctx context.Context) error {
	trip := NewTrip(NewLiveFlags(map[string]any{"booking.online": true}))
	if err := trip.Run(ctx); err != nil {
		return err
	}
	if _, err := trip.Signal(ctx); err != nil {
		return err
	}
	return trip.RecoverReplay(ctx)
}

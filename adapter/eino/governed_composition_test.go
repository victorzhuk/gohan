package eino

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"iter"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std"
)

// compositionTool is a counting worker the governed foreign path wraps; the
// count is the effect ledger the scenario asserts on.
type compositionTool struct {
	mu   sync.Mutex
	spec types.ToolSpec
	ran  int
}

func (t *compositionTool) Spec() types.ToolSpec { return t.spec }

func (t *compositionTool) Call(_ context.Context, _ json.RawMessage) (types.ToolResult, error) {
	t.mu.Lock()
	t.ran++
	t.mu.Unlock()
	return types.ToolResult{Outcome: types.Succeeded}, nil
}

func (t *compositionTool) ranCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.ran
}

// compositionModel replays canned turns in call order; a call past the last
// turn fails loudly instead of inventing behavior.
type compositionModel struct {
	mu    sync.Mutex
	turns [][]types.ModelChunk
	calls int
}

func (m *compositionModel) Profile() types.ModelProfile { return conformanceProfile }

func (m *compositionModel) Generate(_ context.Context, _ types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	m.mu.Lock()
	idx := m.calls
	m.calls++
	turns := m.turns
	m.mu.Unlock()
	return func(yield func(types.ModelChunk, error) bool) {
		if idx >= len(turns) {
			yield(types.ModelChunk{}, fmt.Errorf("composition model: turn %d beyond the scripted %d", idx+1, len(turns)))
			return
		}
		for _, c := range turns[idx] {
			if !yield(c, nil) {
				return
			}
		}
	}
}

// recordingDecider observes every invocation the gate decides and applies
// the scenario's own verdicts; it records, it never widens.
type recordingDecider struct {
	mu   sync.Mutex
	seen []types.ToolUse
	plan map[string]permission.Verdict
}

func (d *recordingDecider) Decide(_ context.Context, inv *permission.ToolInvocation) (types.Decision[permission.Verdict], error) {
	d.mu.Lock()
	d.seen = append(d.seen, inv.Call)
	d.mu.Unlock()
	v, ok := d.plan[inv.Spec.Name]
	if !ok {
		v = permission.DenyVerdict
	}
	return types.Decision[permission.Verdict]{Value: v, Confidence: 1}, nil
}

func (d *recordingDecider) callsFor(name string) []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []string
	for _, c := range d.seen {
		if c.Name == name {
			out = append(out, c.ID)
		}
	}
	return out
}

// governedFixture holds every store handle the scenario reads back through.
type governedFixture struct {
	conv gohan.Conversation
	runs *stores.MemoryRuns
	log  *stores.MemorySessionLog
	el   *stores.MemoryEventLog
	jr   *stores.MemoryJournal
}

// governedStack builds the foreign flow over the shipped eino runtime: the
// runtime factory hands Build a bare ChatModelAgent and the public foreign
// constructor wires Runtime.Step's internal agent — the test installs no
// handler anywhere.
func governedStack(t *testing.T, model types.Model, dec *recordingDecider, workers ...types.Tool) governedFixture {
	t.Helper()
	runs := stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now), stores.WithMemoryRunInfo(types.RunInfoFrom))
	log := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
	cps := stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointClock(time.Now), stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom))
	jr := stores.NewMemoryJournal()
	el := stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))
	lookup := func(name string) (types.ToolSpec, bool) {
		for _, w := range workers {
			if w.Spec().Name == name {
				return w.Spec(), true
			}
		}
		return types.ToolSpec{}, false
	}
	chain := chains.ToolChain{
		{Name: "journal", Kind: chains.KindJournal, Use: std.Journal(jr, std.WithJournalSpecs(lookup))},
	}
	stack, err := gohan.Build(
		gohan.WithModels(model),
		gohan.WithStores(stores.Stores{SessionLog: log, Runs: runs, Checkpoints: cps, Journal: jr}),
		gohan.WithLimits("chat", types.RunLimits{
			MaxTurns: 8, MaxToolCalls: 8, MaxWallClock: time.Minute,
			SoftRatio: 0.8, MaxDepth: 4, MaxParallelChildren: 2, MaxParallelTools: 2,
		}),
		gohan.WithApprovalPolicySource(preemptPolicy{}),
		gohan.WithCredentialSource(preemptCreds{}),
		gohan.WithForeignAgent(gohan.ForeignSpec{
			Request:   gohan.FlowRequest{Name: "chat"},
			Profile:   conformanceProfile.Name,
			Assemble:  passAssemble,
			Tools:     workers,
			ToolChain: chain,
			Decider:   dec,
			NewRuntime: func() runtime.Runtime {
				rt, err := New(context.Background(), &adk.ChatModelAgent{})
				if err != nil {
					t.Fatalf("new eino runtime: %v", err)
				}
				return rt
			},
		}),
	)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	conv, err := gohan.NewForeignConversation(stack, "chat",
		gohan.WithConversationRuns(runs),
		gohan.WithConversationEventLog(el),
	)
	if err != nil {
		t.Fatalf("foreign conversation: %v", err)
	}
	return governedFixture{conv: conv, runs: runs, log: log, el: el, jr: jr}
}

// foreignAdmissions reads the durable admission records out of the session
// journal and buckets the cumulative totals per call id.
func foreignAdmissions(t *testing.T, log *stores.MemorySessionLog, ctx context.Context, session string) map[string][]int {
	t.Helper()
	h, err := log.Load(ctx, session)
	if err != nil {
		t.Fatalf("load session journal: %v", err)
	}
	out := map[string][]int{}
	for _, msg := range h.Messages {
		raw, ok := msg.Meta["gohan.foreign.admission"]
		if !ok {
			continue
		}
		b, err := json.Marshal(raw)
		if err != nil {
			t.Fatalf("admission record encode: %v", err)
		}
		var rec struct {
			AdmittedTotal int    `json:"admitted_total"`
			CallID        string `json:"call_id"`
		}
		if err := json.Unmarshal(b, &rec); err != nil {
			t.Fatalf("admission record decode: %v", err)
		}
		out[rec.CallID] = append(out[rec.CallID], rec.AdmittedTotal)
	}
	return out
}

// foreignToolStarts counts the tool-start journal entries per call id the
// run's event log carries.
func foreignToolStarts(t *testing.T, el *stores.MemoryEventLog, ctx context.Context, runID string) map[string]int {
	t.Helper()
	out := map[string]int{}
	for ev, err := range el.Read(ctx, runID, 0) {
		if err != nil {
			t.Fatalf("event log: %v", err)
		}
		if s, ok := ev.Payload.(types.ToolStarted); ok {
			out[s.Call.ID]++
		}
	}
	return out
}

func TestEinoGovernedComposition(t *testing.T) {
	t.Run("runtime.eino-governed-call-identity", func(t *testing.T) {
		model := &compositionModel{turns: [][]types.ModelChunk{
			{ // turn 1: the side-effect call whose verdict must ask
				{Kind: types.DeltaToolArgs, ToolUse: &types.ToolUse{ID: "call_book_1", Name: "book", Args: jsontext.Value(`{"n":"1"}`)}},
				{Finish: types.FinishToolUse},
			},
			{ // turn 2: one allowed and one denied independent call
				{Kind: types.DeltaToolArgs, ToolUse: &types.ToolUse{ID: "call_search_1", Name: "search", Args: jsontext.Value(`{"q":"x"}`)}},
				{Kind: types.DeltaToolArgs, ToolUse: &types.ToolUse{ID: "call_wipe_1", Name: "wipe", Args: jsontext.Value(`{"t":"1"}`)}},
				{Finish: types.FinishToolUse},
			},
			{ // turn 3: plain completion
				{Kind: types.DeltaText, Delta: "done"},
				{Finish: types.FinishStop},
			},
		}}
		book := &compositionTool{spec: types.ToolSpec{Name: "book", Risk: types.RiskHigh, Effect: types.SideEffect}}
		search := &compositionTool{spec: types.ToolSpec{Name: "search", Effect: types.ReadOnly}}
		wipe := &compositionTool{spec: types.ToolSpec{Name: "wipe", Risk: types.RiskHigh, Effect: types.SideEffect}}
		dec := &recordingDecider{plan: map[string]permission.Verdict{
			"book":   permission.Ask,
			"search": permission.Allow,
			"wipe":   permission.DenyVerdict,
		}}
		f := governedStack(t, model, dec, book, search, wipe)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		t.Cleanup(cancel)
		ctx = preemptPrincipal(ctx)

		// Send: the ask verdict suspends the run with a token and zero effects.
		var token types.ResumeToken
		suspended := false
		sendErr := error(nil)
		for ev, err := range f.conv.Send(ctx, "s1", types.Message{
			Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "go"}},
		}) {
			if s, ok := ev.(types.Suspended); ok {
				suspended = true
				token = s.Token
			}
			if err != nil {
				sendErr = err
			}
		}
		if sendErr != nil {
			t.Fatalf("send: %v", sendErr)
		}
		if !suspended || token == "" {
			t.Fatalf("send ended suspended=%v token=%q, want a HumanApproval suspension carrying the ask token: the backend call id never reached the governed gate through the shipped foreign runtime", suspended, string(token))
		}
		if n := book.ranCount(); n != 0 {
			t.Fatalf("book ran %d times before the verdict, want 0 effects while the ask is pending", n)
		}
		if got := dec.callsFor("book"); len(got) == 0 || got[0] != "call_book_1" {
			t.Fatalf("gate saw book calls %v, want call_book_1 decided under its own backend identity", got)
		}

		// Run records: the parked run belongs to the session and the flow.
		run, err := f.runs.RunForSession(ctx, "s1")
		if err != nil {
			t.Fatalf("run record: %v", err)
		}
		if run.SessionID != "s1" || run.Flow != "chat" || run.RunID == "" {
			t.Fatalf("run record = %+v, want the chat run of session s1 with an id", run)
		}

		// Admission accounting: exactly one prepaid record for the pending call.
		adm := foreignAdmissions(t, f.log, ctx, "s1")
		if got := len(adm["call_book_1"]); got != 1 {
			t.Fatalf("call_book_1 admission records = %d, want exactly one prepaid record before approval", got)
		}
		if len(adm["call_book_1"]) == 1 && adm["call_book_1"][0] != 1 {
			t.Fatalf("call_book_1 admitted total = %v, want the first cumulative slot", adm["call_book_1"])
		}

		// Resume under an authorized principal: the approved call executes
		// exactly once under its persisted id, the allowed call runs, the
		// denied call has no effect.
		rctx, rcancel := context.WithTimeout(context.Background(), 10*time.Second)
		t.Cleanup(rcancel)
		rctx = types.WithPrincipal(rctx, types.Principal{
			Tenant:  "t1",
			Subject: "approver-1",
			Scopes:  []string{types.ScopeSessionRead, types.ScopeSessionWrite, "approve:tool"},
		})
		done := false
		resumeErr := error(nil)
		for ev, err := range f.conv.Resume(rctx, token, gohan.Approve()) {
			if _, ok := ev.(types.Done); ok {
				done = true
			}
			if err != nil {
				resumeErr = err
			}
		}
		if resumeErr != nil {
			t.Fatalf("resume: %v", resumeErr)
		}
		if !done {
			t.Fatal("resume stream ended without Done")
		}
		if n := book.ranCount(); n != 1 {
			t.Fatalf("book ran %d times across the saga, want exactly once under the persisted id", n)
		}
		if n := search.ranCount(); n != 1 {
			t.Fatalf("search ran %d times, want once", n)
		}
		if n := wipe.ranCount(); n != 0 {
			t.Fatalf("wipe ran %d times, want 0 for a denied call", n)
		}

		// Two independent calls reach the gate under separate identities.
		if got := dec.callsFor("search"); len(got) == 0 || got[0] != "call_search_1" {
			t.Fatalf("gate saw search calls %v, want call_search_1 under its own identity", got)
		}
		if got := dec.callsFor("wipe"); len(got) == 0 || got[0] != "call_wipe_1" {
			t.Fatalf("gate saw wipe calls %v, want call_wipe_1 under its own identity", got)
		}
		if bookID, searchID := "call_book_1", "call_search_1"; bookID == searchID {
			t.Fatal("independent calls collapsed onto one id")
		}

		// The journal records each executed call once under its persisted
		// id and holds nothing for the denied call.
		for _, tc := range []struct {
			tool, args, callID string
			want               int
		}{
			{"book", `{"n":"1"}`, "call_book_1", 1},
			{"search", `{"q":"x"}`, "call_search_1", 1},
		} {
			entries, err := f.jr.ByFingerprint(ctx, "s1", std.CanonicalFingerprint(tc.tool, jsontext.Value(tc.args)))
			if err != nil {
				t.Fatalf("journal %s: %v", tc.tool, err)
			}
			completed := 0
			for _, e := range entries {
				if e.State == stores.Completed && e.Key == tc.callID {
					completed++
				}
			}
			if completed != tc.want {
				t.Fatalf("journal holds %d completed entries under %s for %s, want %d", completed, tc.callID, tc.tool, tc.want)
			}
		}
		if entries, err := f.jr.ByFingerprint(ctx, "s1", std.CanonicalFingerprint("wipe", jsontext.Value(`{"t":"1"}`))); err != nil {
			t.Fatalf("journal wipe: %v", err)
		} else if len(entries) != 0 {
			t.Fatalf("journal holds %d entries for the denied wipe call, want none", len(entries))
		}

		// Cumulative admissions: each id admitted exactly once under an
		// increasing total — the resumed pending call replays on its
		// prepaid slot instead of charging again.
		adm = foreignAdmissions(t, f.log, ctx, "s1")
		for _, tc := range []struct {
			callID string
			want   []int
		}{
			{"call_book_1", []int{1}},
			{"call_search_1", []int{2}},
			{"call_wipe_1", []int{3}},
		} {
			got := adm[tc.callID]
			if len(got) != len(tc.want) {
				t.Fatalf("%s admission records = %v, want exactly one record with total %v: a resumed pending call must not count twice", tc.callID, got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("%s admitted total %d, want %d", tc.callID, got[i], tc.want[i])
				}
			}
		}

		// The event journal started each executed call once under its
		// persisted id and never started the denied call.
		starts := foreignToolStarts(t, f.el, ctx, run.RunID)
		if starts["call_book_1"] != 1 || starts["call_search_1"] != 1 {
			t.Fatalf("tool starts = %v, want call_book_1 and call_search_1 started exactly once", starts)
		}
		if starts["call_wipe_1"] != 0 {
			t.Fatalf("tool starts = %v, want no start for the denied call_wipe_1", starts)
		}
	})
}

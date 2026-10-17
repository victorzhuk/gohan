// Command research-report runs an offline research-report demo: a scripted
// model gathers two archived sources through gohan's governed native
// conversation, the context policy clears spent tool results and folds the
// covered prefix into a summary, and the persisted history is loaded back
// through the session log at the end. No network, no API keys.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std"
	stdcontext "github.com/victorzhuk/gohan/std/context"
	stdguard "github.com/victorzhuk/gohan/std/guard"
	"github.com/victorzhuk/gohan/std/limit"
	stdnotes "github.com/victorzhuk/gohan/std/notes"
	stdtokens "github.com/victorzhuk/gohan/std/tokens"
	"github.com/victorzhuk/gohan/std/toolsearch"
)

// Report is what one research-report run produced: the streamed events,
// the fixture tools the batch drew on, the history the governed
// conversation persisted, and the assembled requests it recorded.
type Report struct {
	Events   []types.Event
	Tools    []types.Tool
	History  []types.Message
	Requests []types.ModelRequest
}

// newClearProjection keeps the last two tool results intact and clears
// older ones, so spent archive lookups leave the assembled requests while
// the session log keeps the originals.
func newClearProjection() stdcontext.ClearToolResults {
	c := stdcontext.NewClearToolResults(stdcontext.WithClearEstimator(stdtokens.Heuristic))
	c.Keep = 2
	c.ClearAtLeast = 1
	return c
}

// buildStack assembles the demo stack: the scripted research flow
// registered as a native definition over the fixture tools, stores and
// policy. Each assembled request is recorded through requests.
// stackOption tunes buildStack for one composition: the scripted model,
// the run cost ceiling, and the notes store the fixture tools share.
type stackOption func(*stackCfg)

type stackCfg struct {
	model      types.Model
	maxCost    float64
	notesStore stores.NotesStore
}

func withModel(m types.Model) stackOption { return func(c *stackCfg) { c.model = m } }

func withMaxCost(c float64) stackOption { return func(cfg *stackCfg) { cfg.maxCost = c } }

func withNotesStore(ns stores.NotesStore) stackOption {
	return func(c *stackCfg) { c.notesStore = ns }
}

func buildStack(topic string, requests *[]types.ModelRequest, opts ...stackOption) (*gohan.Stack, stores.Stores, []types.Tool, error) {
	cfg := stackCfg{model: fixtureModel(), maxCost: 1000, notesStore: stores.NewMemoryNotes()}
	for _, opt := range opts {
		opt(&cfg)
	}
	sessions := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
	pair, err := stdnotes.New(cfg.notesStore)
	if err != nil {
		return nil, stores.Stores{}, nil, err
	}
	tools := fixtureTools(pair)
	stack, err := gohan.Build(append(std.Defaults(),
		gohan.WithStores(stores.Stores{SessionLog: sessions, Audit: stores.NewMemoryAuditLog(), EventLog: stores.NewMemoryEventLog()}),
		gohan.WithModels(cfg.model),
		gohan.WithEstimator(stdtokens.Heuristic),
		gohan.WithCompactionGuard(stdguard.NewContextGuard()),
		gohan.WithCompactionFallback(stdcontext.NewTruncate(stdcontext.WithTruncateEstimator(stdtokens.Heuristic))),
		gohan.WithModelMiddleware(limit.Limits(types.RunLimits{
			MaxTurns:     20,
			MaxToolCalls: 40,
			MaxCost:      cfg.maxCost,
			MaxWallClock: time.Minute,
			SoftRatio:    0.8,
		}, types.Pricing{Input: 0.001, Output: 0.002})),
		gohan.WithNativeAgent(gohan.NativeSpec{
			Request: gohan.FlowRequest{Name: "research_report"},
			Profile: "scripted",
			ContextPolicy: stores.ContextPolicy{
				Projections: []stores.Projection{newClearProjection()},
				Compactor: stdcontext.NewSummarize(
					stdcontext.WithSummarizeEstimator(stdtokens.Heuristic),
					stdcontext.WithSummarizeSource(func(_ context.Context, h stores.History, _ types.ModelProfile) ([]types.Block, error) {
						text := fmt.Sprintf("Research so far on %s: %d messages covered.", topic, len(h.Messages))
						if active := discoveredSources(h.Messages); len(active) > 0 {
							text += " Active sources: " + strings.Join(active, ", ") + "."
						}
						return []types.Block{types.Text{Text: text}}, nil
					}),
				),
			},
			Assemble: func(ctx context.Context, in types.AssembleInput) (types.ModelRequest, error) {
				specs, err := std.NarrowTools(in.Tools, toolsearch.DeferredFilter(discoveredSources(in.History)), len(*requests)+1)
				if err != nil {
					return types.ModelRequest{}, err
				}
				req := types.ModelRequest{Messages: slices.Clone(in.History), Tools: specs}
				blocks, err := pair.Provide(ctx, in.Run)
				if err != nil {
					return types.ModelRequest{}, err
				}
				if len(blocks) > 0 {
					req.Messages = append(req.Messages, types.Message{Role: types.RoleUser, Blocks: blocks})
				}
				*requests = append(*requests, req)
				return req, nil
			},
			Tools: tools,
			Decider: policyDecider{
				"search_archive":       permission.Allow,
				"take_notes":           permission.Allow,
				"search_tools":         permission.Allow,
				"source_corpus_batch":  permission.Allow,
				"source_corpus_stream": permission.Allow,
			},
		}),
	)...)
	if err != nil {
		return nil, stores.Stores{}, nil, err
	}
	return stack, stores.Stores{SessionLog: sessions}, tools, nil
}

// Compose builds a stack with the scripted research flow registered as a
// native definition, obtains the governed native conversation for it, and
// streams one send. The context policy projects spent tool results away
// and summarizes the covered prefix; the example persists nothing by hand.
func Compose(ctx context.Context, topic string) (Report, error) {
	var requests []types.ModelRequest
	stack, storeStack, tools, err := buildStack(topic, &requests)
	if err != nil {
		return Report{}, err
	}
	sessions := storeStack.SessionLog
	conv, err := gohan.NewNativeConversation(stack, "research_report",
		gohan.WithConversationRuns(stores.NewMemoryRuns()),
		gohan.WithConversationEventLog(stores.NewMemoryEventLog()),
	)
	if err != nil {
		return Report{}, err
	}
	ctx = types.WithPrincipal(ctx, reportPrincipal())
	// The archive backlog is persisted before the run starts: the compaction
	// fixed point measures the working history, and only store-appended
	// messages carry the canonical ids the durable reload returns.
	if _, err := sessions.Append(ctx, "research", 0, types.Message{
		Role:   types.RoleUser,
		Blocks: []types.Block{types.Text{Text: backlogResult()}},
	}); err != nil {
		return Report{}, err
	}
	var report Report
	for ev, err := range conv.Send(ctx, "research", types.Message{
		Role:   types.RoleUser,
		Blocks: []types.Block{types.Text{Text: topic}},
	}) {
		if err != nil {
			return report, err
		}
		report.Events = append(report.Events, ev)
	}
	// The continue send reloads the persisted history: its first assembly
	// crosses the fixture window and folds the covered prefix — the
	// settled archive-and-note pair among it — from store-canonical
	// messages only, then runs the discovered corpus sources and the
	// final report turn.
	for ev, err := range conv.Continue(ctx, "research") {
		if err != nil {
			return report, err
		}
		report.Events = append(report.Events, ev)
	}
	loaded, err := sessions.Load(ctx, "research")
	if err != nil {
		return report, err
	}
	report.Tools = tools
	report.History = loaded.Messages
	report.Requests = requests
	return report, nil
}

// DetachedReport is what one detached research-report run produced for a
// client that dropped mid-run and reattached: the run's id, the Seq cursor
// the client had consumed when it dropped, every event the run logged with
// its metadata, the events Attach delivered after the cursor, every request
// the original run assembled, the requests the resumed run assembled, and
// the persisted history.
type DetachedReport struct {
	RunID           string
	Cursor          int64
	Logged          []stores.Event
	Reattached      []types.Event
	Requests        []types.ModelRequest
	ResumedRequests []types.ModelRequest
	History         []types.Message
	Suspension      stores.Event
	ResumeEvents    []stores.Event
	TokenConsumed   bool
}

// hasDurableCompaction reports whether the history holds a published
// compaction message.
func hasDurableCompaction(history []types.Message) bool {
	for _, m := range history {
		for _, b := range m.Blocks {
			if _, ok := b.(types.Compaction); ok {
				return true
			}
		}
	}
	return false
}

// ComposeDetached runs the research composition detached: gohan.Detached()
// mints the run, Send starts it and returns without streaming, a client
// consumes a prefix of the logged events, drops at a known Seq, and
// reattaches through Attach with that cursor. Continue then reloads the
// durable history, publishes the compaction, and suspends on its fourth
// provider entry; Resume consumes the single-use token and completes the
// run from the same conversation.

// capturingRuns wraps the runs store so the example learns the run id the
// detached Send minted: a detached Send returns without streaming, so the
// caller cannot read the id from the iterator.
type capturingRuns struct {
	stores.Runs
	mu sync.Mutex
	id string
}

func (r *capturingRuns) Start(ctx context.Context, run stores.Run, ttl time.Duration) (stores.Lease, error) {
	l, err := r.Runs.Start(ctx, run, ttl)
	if err == nil {
		r.mu.Lock()
		r.id = run.RunID
		r.mu.Unlock()
	}
	return l, err
}

func (r *capturingRuns) captured() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.id
}

// waitLogged drains the event log until the run records a terminal event
// or the deadline passes.
func waitLogged(ctx context.Context, log stores.EventLog, runID string) ([]stores.Event, error) {
	deadline := time.Now().Add(10 * time.Second)
	for {
		var logged []stores.Event
		done := false
		for ev, err := range log.Read(ctx, runID, 0) {
			if err != nil {
				return nil, err
			}
			logged = append(logged, ev)
			switch ev.Payload.(type) {
			case types.Done, types.Suspended:
				done = true
			}
		}
		if done {
			return logged, nil
		}
		if time.Now().After(deadline) {
			return logged, fmt.Errorf("detached run %s recorded no terminal event within 10s", runID)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func ComposeDetached(ctx context.Context, topic string) (DetachedReport, error) {
	var requests []types.ModelRequest
	notesStore := stores.NewMemoryNotes()
	model := detachedModel()
	stack, storeStack, _, err := buildStack(topic, &requests, withModel(model), withNotesStore(notesStore))
	if err != nil {
		return DetachedReport{}, err
	}
	sessions := storeStack.SessionLog
	log := stores.NewMemoryEventLog()
	runs := &capturingRuns{Runs: stores.NewMemoryRuns()}
	checkpoints := stores.NewMemoryCheckpoints()
	conv, err := gohan.NewNativeConversation(stack, "research_report",
		gohan.WithConversationRuns(runs),
		gohan.WithConversationEventLog(log),
		gohan.WithConversationCheckpoints(checkpoints),
		gohan.Detached(),
	)
	if err != nil {
		return DetachedReport{}, err
	}
	ctx = types.WithPrincipal(ctx, reportPrincipal())
	// The archive backlog is persisted before the run starts: only
	// store-appended messages carry the canonical ids the durable reload
	// returns, and the compaction folds only store-canonical history.
	if _, err := sessions.Append(ctx, "research", 0, types.Message{
		Role:   types.RoleUser,
		Blocks: []types.Block{types.Text{Text: backlogResult()}},
	}); err != nil {
		return DetachedReport{}, err
	}
	for _, err := range conv.Send(ctx, "research", types.Message{
		Role:   types.RoleUser,
		Blocks: []types.Block{types.Text{Text: topic}},
	}) {
		if err != nil {
			return DetachedReport{}, err
		}
	}
	runID := runs.captured()
	if runID == "" {
		return DetachedReport{}, errors.New("detached send minted no run id")
	}
	logged, err := waitLogged(ctx, log, runID)
	if err != nil {
		return DetachedReport{}, err
	}
	var rep DetachedReport
	rep.RunID = runID
	rep.Logged = logged
	if len(logged) < 2 {
		return rep, fmt.Errorf("run %s logged %d events: need at least two to drop before the tail", runID, len(logged))
	}
	final := logged[len(logged)-1]
	done, ok := final.Payload.(types.Done)
	if !ok || done.Reason != types.StopCompleted {
		return rep, fmt.Errorf("run %s ended in %T: want a completed Done", runID, final.Payload)
	}
	// The client consumes a prefix of the logged events and drops at a
	// known Seq before the logged tail.
	rep.Cursor = logged[len(logged)-2].Meta.Seq
	for ev, err := range conv.Attach(ctx, runID, rep.Cursor) {
		if err != nil {
			return rep, err
		}
		rep.Reattached = append(rep.Reattached, ev)
	}
	// Continue reloads the durable history, publishes the compaction over
	// store-canonical messages, and suspends the run on its fourth provider
	// entry: the fixture model suspends there before producing a response.
	var continueEvents []types.Event
	for ev, err := range conv.Continue(ctx, "research") {
		if err != nil {
			return rep, fmt.Errorf("continue: %w", err)
		}
		continueEvents = append(continueEvents, ev)
	}
	var suspension types.Suspended
	suspensions := 0
	for _, ev := range continueEvents {
		if e, ok := ev.(types.Suspended); ok {
			suspension = e
			suspensions++
			continue
		}
		if _, ok := ev.(types.Done); ok {
			return rep, errors.New("the continue run completed: no suspension occurred")
		}
	}
	if suspensions != 1 {
		return rep, fmt.Errorf("the continue run recorded %d suspensions: want exactly one", suspensions)
	}
	if suspension.Reason != types.AwaitingControl {
		return rep, fmt.Errorf("suspension reason %q: want %q", suspension.Reason, types.AwaitingControl)
	}
	if suspension.Token == "" {
		return rep, errors.New("the suspension carries an empty resume token")
	}
	continueRunID := runs.captured()
	if continueRunID == "" {
		return rep, errors.New("the continue run minted no run id")
	}
	if continueRunID == runID {
		return rep, fmt.Errorf("continue reused the send run id %s: it must mint a distinct run", runID)
	}
	var continueLogged []stores.Event
	for ev, err := range log.Read(ctx, continueRunID, 0) {
		if err != nil {
			return rep, err
		}
		continueLogged = append(continueLogged, ev)
	}
	for _, e := range continueLogged {
		if s, ok := e.Payload.(types.Suspended); ok && s.Token == suspension.Token {
			rep.Suspension = e
			break
		}
	}
	if rep.Suspension.Payload == nil {
		return rep, fmt.Errorf("run %s logged no suspension record for token %s", continueRunID, suspension.Token)
	}
	cp, err := checkpoints.Peek(ctx, suspension.Token)
	if err != nil {
		return rep, fmt.Errorf("peek resume token: %w", err)
	}
	if cp.RunID != continueRunID {
		return rep, fmt.Errorf("checkpoint names run %s: want the suspended run %s", cp.RunID, continueRunID)
	}
	if cp.SessionID != "research" {
		return rep, fmt.Errorf("checkpoint names session %q: want research", cp.SessionID)
	}
	if cp.Flow != "research_report" {
		return rep, fmt.Errorf("checkpoint names flow %q: want research_report", cp.Flow)
	}
	if cp.Reason != types.AwaitingControl {
		return rep, fmt.Errorf("checkpoint reason %q: want %q", cp.Reason, types.AwaitingControl)
	}
	// The durable history the Resume rebuilds from is frozen here: the
	// compaction the Continue run published is part of it.
	loaded, err := sessions.Load(ctx, "research")
	if err != nil {
		return rep, err
	}
	rep.History = loaded.Messages
	if !hasDurableCompaction(rep.History) {
		return rep, errors.New("the session history carries no durable compaction before the Resume")
	}
	// The four provider entries the suspension covers are the opening
	// send's three turns plus the suspended Continue assembly.
	if len(model.requests) != 4 {
		return rep, fmt.Errorf("the fixture recorded %d provider captures before the suspension: want four", len(model.requests))
	}
	rep.Requests = slices.Clone(model.requests)
	var delivered []types.Event
	for ev, err := range conv.Resume(ctx, suspension.Token, stores.ResumeInput{
		Verdict: stores.VerdictApprove,
	}) {
		if err != nil {
			return rep, fmt.Errorf("resume: %w", err)
		}
		delivered = append(delivered, ev)
	}
	if len(model.requests) != 6 {
		return rep, fmt.Errorf("the fixture recorded %d provider captures after the resume: want six", len(model.requests))
	}
	rep.ResumedRequests = slices.Clone(model.requests[4:])
	// The resumed run's durable suffix starts strictly after the
	// suspension record and must mirror what the Resume iterator
	// delivered. The terminal flush can land just after the iterator
	// returns, so poll the log within the same bound waitLogged uses.
	deadline := time.Now().Add(10 * time.Second)
	var suffix []stores.Event
	for {
		suffix = nil
		for ev, err := range log.Read(ctx, continueRunID, 0) {
			if err != nil {
				return rep, err
			}
			if ev.Meta.Seq > rep.Suspension.Meta.Seq {
				suffix = append(suffix, ev)
			}
		}
		if len(suffix) > 0 {
			if _, ok := suffix[len(suffix)-1].Payload.(types.Done); ok {
				break
			}
		}
		if time.Now().After(deadline) {
			return rep, fmt.Errorf("run %s logged no completing suffix after the suspension Seq %d within 10s", continueRunID, rep.Suspension.Meta.Seq)
		}
		time.Sleep(2 * time.Millisecond)
	}
	if len(suffix) != len(delivered) {
		return rep, fmt.Errorf("the resumed suffix holds %d events: the resume delivered %d", len(suffix), len(delivered))
	}
	dones := 0
	for i, e := range suffix {
		rep.ResumeEvents = append(rep.ResumeEvents, e)
		loggedPayload, merr := json.Marshal(e.Payload)
		deliveredPayload, derr := json.Marshal(delivered[i])
		if merr != nil || derr != nil {
			return rep, fmt.Errorf("marshal resumed event %d: %v %v", i, merr, derr)
		}
		if !bytes.Equal(loggedPayload, deliveredPayload) {
			return rep, fmt.Errorf("resumed event %d logged %s: the resume delivered %s", i, loggedPayload, deliveredPayload)
		}
		if e.Meta.RunID != continueRunID {
			return rep, fmt.Errorf("resumed event %d belongs to run %s: want the suspended run %s", i, e.Meta.RunID, continueRunID)
		}
		if s, ok := e.Payload.(types.Suspended); ok {
			return rep, fmt.Errorf("resumed event %d suspends again with reason %q: the resume must complete", i, s.Reason)
		}
		if d, ok := e.Payload.(types.Done); ok {
			dones++
			if d.Reason != types.StopCompleted {
				return rep, fmt.Errorf("resumed Done carries reason %q: want StopCompleted", d.Reason)
			}
		}
	}
	if dones != 1 {
		return rep, fmt.Errorf("the resumed suffix holds %d Done events: want exactly one completing terminal", dones)
	}
	if _, ok := suffix[len(suffix)-1].Payload.(types.Done); !ok {
		return rep, fmt.Errorf("the resumed log suffix ends in %T: it must end in the completing Done", suffix[len(suffix)-1].Payload)
	}
	_, err = checkpoints.Peek(ctx, suspension.Token)
	rep.TokenConsumed = errors.Is(err, types.ErrTokenConsumed)
	if !rep.TokenConsumed {
		return rep, fmt.Errorf("post-resume peek returned %v: want a consumed token", err)
	}
	return rep, nil
}

// FanoutReport reports a wider retrieval fan-out against the declared cost
// ceiling: whether the run refused the fan-out, the refusal reason, and the
// parent's accumulated total.
type FanoutReport struct {
	Refused    bool
	Reason     string
	ParentCost float64
}

// ComposeWiderFanout composes the same research flow with a retrieval
// fan-out wider than the declared ceiling admits and reports the refusal.
// The parent cost is the run's own accounting at the moment the MaxCost
// limit refused the next model call.
func ComposeWiderFanout(ctx context.Context, topic string) (FanoutReport, error) {
	batch := types.ToolUse{ID: "call_wide_batch", Name: "source_corpus_batch", Args: jsontextArgs(`{"kind":"batch"}`)}
	stream := types.ToolUse{ID: "call_wide_stream", Name: "source_corpus_stream", Args: jsontextArgs(`{"kind":"stream"}`)}
	search := types.ToolUse{ID: "call_wide_search_tools", Name: toolsearch.SearchToolsName, Args: jsontextArgs(`{"query":"corpus"}`)}
	calls := append(fixtureBatch(), search, batch, stream, batch, stream)
	m := &scriptedTurns{turns: [][]types.ModelChunk{
		{{ToolUse: &calls[0]}, {ToolUse: &calls[1]}, {Finish: types.FinishToolUse, Usage: turnUsage(160, 40)}},
		{{ToolUse: &calls[2]}, {Finish: types.FinishToolUse, Usage: turnUsage(140, 30)}},
		{{ToolUse: &calls[3]}, {Finish: types.FinishToolUse, Usage: turnUsage(200, 60)}},
		{{ToolUse: &calls[4]}, {Finish: types.FinishToolUse, Usage: turnUsage(200, 60)}},
		{{ToolUse: &calls[5]}, {Finish: types.FinishToolUse, Usage: turnUsage(200, 60)}},
		{{ToolUse: &calls[6]}, {Finish: types.FinishToolUse, Usage: turnUsage(200, 60)}},
		{{Kind: types.DeltaText, Delta: "Wide report."}, {Finish: types.FinishStop, Usage: turnUsage(210, 70)}},
	}}
	var requests []types.ModelRequest
	stack, _, _, err := buildStack(topic, &requests, withModel(m), withMaxCost(1.0))
	if err != nil {
		return FanoutReport{}, err
	}
	conv, err := gohan.NewNativeConversation(stack, "research_report",
		gohan.WithConversationRuns(stores.NewMemoryRuns()),
		gohan.WithConversationEventLog(stores.NewMemoryEventLog()),
	)
	if err != nil {
		return FanoutReport{}, err
	}
	ctx = types.WithPrincipal(ctx, reportPrincipal())
	var rep FanoutReport
	var refusal error
	for ev, err := range conv.Send(ctx, "research", types.Message{
		Role:   types.RoleUser,
		Blocks: []types.Block{types.Text{Text: topic}},
	}) {
		if err != nil {
			refusal = err
			continue
		}
		if done, ok := ev.(types.Done); ok {
			rep.ParentCost = done.Cost
		}
	}
	var exceeded *types.LimitExceededError
	if refusal != nil {
		if errors.As(refusal, &exceeded) && exceeded.Limit == "MaxCost" {
			rep.Refused = true
			rep.Reason = refusal.Error()
			rep.ParentCost = exceeded.Value
			return rep, nil
		}
		return rep, refusal
	}
	return rep, nil
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "explain" {
		if _, err := os.Stdout.Stat(); err == nil && !isTerminal(os.Stdout) {
			if err := runExplain(os.Stdout); err != nil {
				fmt.Fprintln(os.Stderr, "research-report:", err)
				os.Exit(1)
			}
			return
		}
		var buf bytes.Buffer
		if err := runExplain(&buf); err != nil {
			fmt.Fprintln(os.Stderr, "research-report:", err)
			os.Exit(1)
		}
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, buf.Bytes(), "", "  "); err == nil {
			fmt.Println(pretty.String())
		} else {
			fmt.Print(buf.String())
		}
		return
	}
	report, err := Compose(context.Background(), "Research signal pipelines: search the archive and take notes.")
	if err != nil {
		fmt.Fprintln(os.Stderr, "research-report:", err)
		os.Exit(1)
	}
	for _, ev := range report.Events {
		if d, ok := ev.(types.TextDelta); ok {
			fmt.Print(d.Delta)
		}
	}
	fmt.Println()
	for _, m := range report.History {
		for _, b := range m.Blocks {
			if res, ok := b.(types.ToolResult); ok {
				fmt.Printf("source: %s: %v\n", res.ID, res.Outcome)
			}
		}
	}
}

// isTerminal reports whether w is a character device such as an
// interactive terminal.
func isTerminal(w *os.File) bool {
	st, err := w.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}

// runExplain prints the resolved configuration of the research_report flow
// exactly as std.ExplainHandler serves it: no provider call runs and no run
// budget is spent. The output is JSON when stdout is not a terminal.
func runExplain(w io.Writer) error {
	stack, _, _, err := buildStack("windowed joins and exactly-once sinks", &[]types.ModelRequest{})
	if err != nil {
		return err
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/explain?flow=research_report", nil)
	std.ExplainHandler(stack).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		return fmt.Errorf("explain: status %d: %s", rec.Code, rec.Body.String())
	}
	_, err = w.Write(rec.Body.Bytes())
	return err
}

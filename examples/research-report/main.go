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
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
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
func buildStack(topic string, requests *[]types.ModelRequest) (*gohan.Stack, stores.Stores, []types.Tool, error) {
	sessions := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
	pair, err := stdnotes.New(stores.NewMemoryNotes())
	if err != nil {
		return nil, stores.Stores{}, nil, err
	}
	tools := fixtureTools(pair)
	stack, err := gohan.Build(append(std.Defaults(),
		gohan.WithStores(stores.Stores{SessionLog: sessions, Audit: stores.NewMemoryAuditLog(), EventLog: stores.NewMemoryEventLog()}),
		gohan.WithModels(fixtureModel()),
		gohan.WithEstimator(stdtokens.Heuristic),
		gohan.WithCompactionGuard(stdguard.NewContextGuard()),
		gohan.WithCompactionFallback(stdcontext.NewTruncate(stdcontext.WithTruncateEstimator(stdtokens.Heuristic))),
		gohan.WithModelMiddleware(limit.Limits(types.RunLimits{
			MaxTurns:     20,
			MaxToolCalls: 40,
			MaxCost:      100,
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
	loaded, err := sessions.Load(ctx, "research")
	if err != nil {
		return report, err
	}
	report.Tools = tools
	report.History = loaded.Messages
	report.Requests = requests
	return report, nil
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

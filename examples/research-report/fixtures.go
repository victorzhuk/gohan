package main

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"iter"
	"strings"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/notes"
	"github.com/victorzhuk/gohan/std/toolsearch"
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

// reportPrincipal is the demo analyst: it may read and write the session
// but carries no approve scope, so tool calls pass through the fixture
// policy below.
func reportPrincipal() types.Principal {
	return types.Principal{
		Tenant:  "local",
		Subject: "analyst",
		Scopes:  []string{types.ScopeSessionRead, types.ScopeSessionWrite},
	}
}

// fixtureBatch is the tool batch the scripted model asks for on its first
// turn: one archive lookup and one notes write, both satisfied offline.
func fixtureBatch() []types.ToolUse {
	return []types.ToolUse{
		{ID: "call_search_archive", Name: "search_archive", Args: jsontextArgs(`{"topic":"signal pipelines"}`)},
		{ID: "call_take_notes", Name: "take_notes", Args: jsontextArgs(`{"note":"compare batch vs stream"}`)},
	}
}

func jsontextArgs(s string) (v jsontext.Value) {
	return jsontext.Value(s)
}

// fixtureTools builds the offline tool doubles: the two always-loaded
// sources the scripted batch uses plus the search_tools meta-tool over
// the deferred catalog below. take_notes stores through the session notes
// pair, so the SlotSession provider reads the note back. Every result is
// fixed here, so a run never touches a network or a filesystem.
func fixtureTools(pair *notes.Notes) []types.Tool {
	search := gohantest.NewFakeTool("search_archive",
		gohantest.WithToolSpec(func(s *types.ToolSpec) {
			s.Description = "Look up archived research notes on a topic."
			s.Effect = types.ReadOnly
		}),
		gohantest.WithToolResult(types.ToolResult{
			Outcome: types.Succeeded,
			Content: []types.Block{types.Text{Text: archiveResult()}},
		}),
	)
	notesTool, err := gohan.NewTool("take_notes",
		"Append a research note.",
		func(ctx context.Context, args struct {
			Note string `json:"note"`
		}) (string, error) {
			raw, err := json.Marshal(map[string]string{"notes": args.Note})
			if err != nil {
				return "", err
			}
			if _, err := pair.Call(ctx, raw); err != nil {
				return "", err
			}
			return "note stored", nil
		},
		gohan.WithEffect(types.SideEffect),
	)
	if err != nil {
		// Unreachable: the name and the argument schema are always valid.
		panic(err)
	}
	meta, err := toolsearch.NewSearchTools(deferredRegistry)
	if err != nil {
		// Unreachable: the reserved search_tools name is always valid.
		panic(err)
	}
	corpus := sourceTools(deferredCatalog, "source_corpus_batch", "source_corpus_stream")
	return []types.Tool{search, notesTool, meta, corpus[0], corpus[1]}
}

// archiveResult is the archived lookup payload: the asserted summary line
// followed by retrieved context sized so the turn that settles the archive
// and note pair still fits the fixture window, while the discovery turn
// after it crosses the window and publishes the compaction. By then the
// take_notes pair is settled history, so the fold covers the note.
func archiveResult() string {
	return "2 notes found: windowed joins, exactly-once sinks. " + strings.Repeat("archived retrieval context ", 150)
}

// backlogResult is the pre-run archive backlog: seeded store-canonical
// history small enough that the first assembled request stays under the
// fixture window, so the compaction never folds before the run writes its
// note.
func backlogResult() string {
	return "archived backlog: prior retrieval sessions. " + strings.Repeat("archived backlog digest ", 20)
}

// sourceTools materializes fake tools for the named catalog entries, so
// the discovered definitions carry executable bodies over the same specs
// the registry reports.
func sourceTools(catalog []types.ToolSpec, names ...string) []types.Tool {
	out := make([]types.Tool, 0, len(names))
	for _, name := range names {
		var spec types.ToolSpec
		for _, s := range catalog {
			if s.Name == name {
				spec = s
				break
			}
		}
		out = append(out, gohantest.NewFakeTool(name,
			gohantest.WithToolSpec(func(s *types.ToolSpec) {
				*s = spec
			}),
			gohantest.WithToolResult(types.ToolResult{
				Outcome: types.Succeeded,
				Content: []types.Block{types.Text{Text: name + " digest: corpus retrieved"}},
			}),
		))
	}
	return out
}

// deferredCatalog is the deterministic table of deferred source tools the
// demo discovers through search_tools. Every entry is read-only and
// Deferred, so the per-turn filter keeps them out of the assembled set
// until the run activates them.
var deferredCatalog = []types.ToolSpec{
	{Name: "source_archives_alpha", Description: "Archived alpha-series research digests.", Effect: types.ReadOnly, Deferred: true},
	{Name: "source_archives_beta", Description: "Archived beta-series research digests.", Effect: types.ReadOnly, Deferred: true},
	{Name: "source_archives_gamma", Description: "Archived gamma-series research digests.", Effect: types.ReadOnly, Deferred: true},
	{Name: "source_archives_delta", Description: "Archived delta-series research digests.", Effect: types.ReadOnly, Deferred: true},
	{Name: "source_bibliography_local", Description: "Local bibliography cross-references.", Effect: types.ReadOnly, Deferred: true},
	{Name: "source_bibliography_shared", Description: "Shared-team bibliography entries.", Effect: types.ReadOnly, Deferred: true},
	{Name: "source_citations_unverified", Description: "Citation list awaiting verification.", Effect: types.ReadOnly, Deferred: true},
	{Name: "source_citations_verified", Description: "Verified citation records.", Effect: types.ReadOnly, Deferred: true},
	{Name: "source_corpus_batch", Description: "Batch-processing corpus documents.", Effect: types.ReadOnly, Deferred: true},
	{Name: "source_corpus_stream", Description: "Stream-processing corpus documents.", Effect: types.ReadOnly, Deferred: true},
	{Name: "source_datasets_curated", Description: "Curated dataset index cards.", Effect: types.ReadOnly, Deferred: true},
	{Name: "source_datasets_raw", Description: "Raw dataset manifests.", Effect: types.ReadOnly, Deferred: true},
	{Name: "source_fieldnotes_2024", Description: "2024 field-note entries.", Effect: types.ReadOnly, Deferred: true},
	{Name: "source_fieldnotes_2025", Description: "2025 field-note entries.", Effect: types.ReadOnly, Deferred: true},
	{Name: "source_glossary_terms", Description: "Research glossary term sheets.", Effect: types.ReadOnly, Deferred: true},
	{Name: "source_glossary_acronyms", Description: "Acronym expansion table.", Effect: types.ReadOnly, Deferred: true},
	{Name: "source_interviews_transcripts", Description: "Interview transcript archive.", Effect: types.ReadOnly, Deferred: true},
	{Name: "source_interviews_notes", Description: "Interview margin notes.", Effect: types.ReadOnly, Deferred: true},
	{Name: "source_journals_internal", Description: "Internal journal abstracts.", Effect: types.ReadOnly, Deferred: true},
	{Name: "source_journals_external", Description: "External journal abstracts.", Effect: types.ReadOnly, Deferred: true},
	{Name: "source_letters_scans", Description: "Scanned letter metadata.", Effect: types.ReadOnly, Deferred: true},
	{Name: "source_letters_indexes", Description: "Letter index pages.", Effect: types.ReadOnly, Deferred: true},
	{Name: "source_maps_annotations", Description: "Annotated map overlays.", Effect: types.ReadOnly, Deferred: true},
	{Name: "source_maps_tiles", Description: "Basemap tile listings.", Effect: types.ReadOnly, Deferred: true},
	{Name: "source_notes_margin", Description: "Margin-note compilation.", Effect: types.ReadOnly, Deferred: true},
	{Name: "source_notes_meeting", Description: "Meeting-note summaries.", Effect: types.ReadOnly, Deferred: true},
	{Name: "source_papers_preprints", Description: "Preprint paper records.", Effect: types.ReadOnly, Deferred: true},
	{Name: "source_papers_peerreviewed", Description: "Peer-reviewed paper records.", Effect: types.ReadOnly, Deferred: true},
	{Name: "source_reports_quarterly", Description: "Quarterly progress reports.", Effect: types.ReadOnly, Deferred: true},
	{Name: "source_reports_annual", Description: "Annual summary reports.", Effect: types.ReadOnly, Deferred: true},
	{Name: "source_surveys_responses", Description: "Survey response exports.", Effect: types.ReadOnly, Deferred: true},
	{Name: "source_surveys_questions", Description: "Survey question banks.", Effect: types.ReadOnly, Deferred: true},
	{Name: "source_tables_appendix", Description: "Appendix data tables.", Effect: types.ReadOnly, Deferred: true},
	{Name: "source_tables_supplement", Description: "Supplementary data tables.", Effect: types.ReadOnly, Deferred: true},
	{Name: "source_telemetry_digests", Description: "Telemetry digest summaries.", Effect: types.ReadOnly, Deferred: true},
	{Name: "source_telemetry_traces", Description: "Sampled trace listings.", Effect: types.ReadOnly, Deferred: true},
	{Name: "source_theses_drafts", Description: "Draft thesis chapters.", Effect: types.ReadOnly, Deferred: true},
	{Name: "source_theses_final", Description: "Final thesis records.", Effect: types.ReadOnly, Deferred: true},
	{Name: "source_worklogs_daily", Description: "Daily work-log entries.", Effect: types.ReadOnly, Deferred: true},
	{Name: "source_worklogs_weekly", Description: "Weekly work-log digests.", Effect: types.ReadOnly, Deferred: true},
}

// deferredRegistry is the registry closure handed to search_tools: it
// reports the deferred catalog verbatim on every call.
func deferredRegistry() []types.ToolSpec {
	return deferredCatalog
}

// fixtureModel scripts a discovery run across two sends: the first send
// takes the always-loaded batch, runs the search_tools discovery, and
// closes with an interim digest sized to stay under the fixture window;
// the second send reloads the persisted history, so its first assembly
// crosses the window and publishes the compaction before the discovered
// corpus sources run and the final report lands. The fold lands on the
// reloaded store-canonical history, never on run-local messages.
func fixtureModel() types.Model {
	calls := append(fixtureBatch(),
		types.ToolUse{ID: "call_search_tools", Name: toolsearch.SearchToolsName, Args: jsontextArgs(`{"query":"corpus"}`)},
		types.ToolUse{ID: "call_source_corpus_batch", Name: "source_corpus_batch", Args: jsontextArgs(`{"kind":"batch"}`)},
		types.ToolUse{ID: "call_source_corpus_stream", Name: "source_corpus_stream", Args: jsontextArgs(`{"kind":"stream"}`)},
	)
	return &scriptedTurns{turns: [][]types.ModelChunk{
		{{ToolUse: &calls[0]}, {ToolUse: &calls[1]}, {Finish: types.FinishToolUse, Usage: turnUsage(160, 40)}},
		{{ToolUse: &calls[2]}, {Finish: types.FinishToolUse, Usage: turnUsage(140, 30)}},
		{{Kind: types.DeltaText, Delta: interimDigest()}, {Finish: types.FinishStop, Usage: turnUsage(180, 45)}},
		{{ToolUse: &calls[3]}, {ToolUse: &calls[4]}, {Finish: types.FinishToolUse, Usage: turnUsage(180, 45)}},
		{{Kind: types.DeltaText, Delta: "Report: windowed joins and exactly-once sinks are the two archived threads; noted the batch-vs-stream comparison."}, {Finish: types.FinishStop, Usage: turnUsage(210, 70)}},
	}}
}

// interimDigest is the first send's closing assistant text: small enough
// that the discovery turn that precedes it stays under the fixture
// window, large enough that the reloaded send that follows crosses the
// window and folds the covered prefix past the settled note pair.
func interimDigest() string {
	return "Interim digest: the archived threads compare batch against stream. " + strings.Repeat("interim findings paragraph ", 221)
}

func turnUsage(in, out int) *types.Usage {
	return &types.Usage{InputTokens: in, OutputTokens: out}
}

// discoveredSources recomputes the active deferred set from the persisted
// discovery: every search_tools use in history re-runs its query against
// the catalog, so the selection is a pure function of what the session
// log holds.
func discoveredSources(history []types.Message) []string {
	var queries []string
	for _, m := range history {
		for _, b := range m.Blocks {
			u, ok := b.(types.ToolUse)
			if !ok || u.Name != toolsearch.SearchToolsName {
				continue
			}
			var args struct {
				Query string `json:"query"`
			}
			if err := json.Unmarshal([]byte(u.Args), &args); err != nil {
				continue
			}
			queries = append(queries, args.Query)
		}
	}
	var active []string
	for _, spec := range deferredCatalog {
		found := false
		for _, q := range queries {
			if matchesQuery(spec, q) {
				found = true
				break
			}
		}
		if !found && historyNamesSource(history, spec.Name) {
			found = true
		}
		if found {
			active = toolsearch.Activate(active, spec.Name)
		}
	}
	return active
}

// historyNamesSource reports whether the persisted text carries the source
// name: the summarized coverage names every source the covered prefix
// discovered, so activation survives a compaction fold of the discovery.
func historyNamesSource(history []types.Message, name string) bool {
	for _, m := range history {
		for _, b := range m.Blocks {
			var blocks []types.Block
			switch v := b.(type) {
			case types.Text:
				blocks = []types.Block{v}
			case types.Compaction:
				blocks = v.Summary
			default:
				continue
			}
			for _, sb := range blocks {
				if tx, ok := sb.(types.Text); ok && strings.Contains(tx.Text, name) {
					return true
				}
			}
		}
	}
	return false
}

func matchesQuery(spec types.ToolSpec, query string) bool {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return false
	}
	return strings.Contains(strings.ToLower(spec.Name+" "+spec.Description), q)
}

// scriptedTurns replays fixed chunk sequences, one per model call. It is
// safe for one sequential run, which is all a demo needs.
type scriptedTurns struct {
	turns [][]types.ModelChunk
	i     int
}

func (m *scriptedTurns) Profile() types.ModelProfile {
	return types.ModelProfile{Name: "scripted", ContextWindow: 2000}
}

func (m *scriptedTurns) Generate(_ context.Context, _ types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		if m.i >= len(m.turns) {
			yield(types.ModelChunk{}, errors.New("gohantest: no scripted turns left"))
			return
		}
		for _, c := range m.turns[m.i] {
			if !yield(c, nil) {
				return
			}
		}
		m.i++
	}
}

// suspendingModel wraps the scripted fixture and records every
// provider-entry request: on the fourth entry it suspends the run before
// the embedded model produces anything, so the checkpoint captures the
// turn boundary and Resume replays the same scripted turns.
type suspendingModel struct {
	types.Model
	requests []types.ModelRequest
}

// detachedModel scripts the detached composition: the opening send replays
// the batch, discovery and digest turns of the plain fixture, Continue
// suspends on its fourth provider entry, and Resume consumes the two turns
// the suspension held back.
func detachedModel() *suspendingModel {
	return &suspendingModel{Model: fixtureModel()}
}

func (m *suspendingModel) Generate(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		m.requests = append(m.requests, req)
		if len(m.requests) == 4 {
			yield(types.ModelChunk{}, &types.SuspendError{Reason: types.AwaitingControl})
			return
		}
		for c, err := range m.Model.Generate(ctx, req) {
			if !yield(c, err) {
				return
			}
		}
	}
}

// policyDecider is the fixture permission policy of the demo: archive
// lookups and note writes pass, so the run stays offline and every tool
// result is the fixed fixture payload.
type policyDecider map[string]permission.Verdict

func (p policyDecider) Decide(_ context.Context, inv *permission.ToolInvocation) (types.Decision[permission.Verdict], error) {
	v, ok := p[inv.Spec.Name]
	if !ok {
		return types.Decision[permission.Verdict]{Value: permission.Ask}, nil
	}
	return types.Decision[permission.Verdict]{Value: v, Confidence: 1}, nil
}

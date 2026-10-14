package main

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"iter"

	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/types"
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
// the deferred catalog below. Every result is fixed here, so a run never
// touches a network or a filesystem.
func fixtureTools() []types.Tool {
	search := gohantest.NewFakeTool("search_archive",
		gohantest.WithToolSpec(func(s *types.ToolSpec) {
			s.Description = "Look up archived research notes on a topic."
			s.Effect = types.ReadOnly
		}),
		gohantest.WithToolResult(types.ToolResult{
			Outcome: types.Succeeded,
			Content: []types.Block{types.Text{Text: "2 notes found: windowed joins, exactly-once sinks"}},
		}),
	)
	notes := gohantest.NewFakeTool("take_notes",
		gohantest.WithToolSpec(func(s *types.ToolSpec) {
			s.Description = "Append a research note."
			s.Effect = types.SideEffect
			s.Risk = types.RiskLow
		}),
		gohantest.WithToolResult(types.ToolResult{
			Outcome: types.Succeeded,
			Content: []types.Block{types.Text{Text: "note stored"}},
		}),
	)
	meta, err := toolsearch.NewSearchTools(deferredRegistry)
	if err != nil {
		// Unreachable: the reserved search_tools name is always valid.
		panic(err)
	}
	return []types.Tool{search, notes, meta}
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

// fixtureModel scripts one multi-call tool turn followed by the final
// report text; both batch calls arrive in a single model turn.
func fixtureModel() types.Model {
	calls := fixtureBatch()
	return &scriptedTurns{turns: [][]types.ModelChunk{
		{{ToolUse: &calls[0]}, {ToolUse: &calls[1]}, {Finish: types.FinishToolUse}},
		{{Kind: types.DeltaText, Delta: "Report: windowed joins and exactly-once sinks are the two archived threads; noted the batch-vs-stream comparison."}, {Finish: types.FinishStop}},
	}}
}

// scriptedTurns replays fixed chunk sequences, one per model call. It is
// safe for one sequential run, which is all a demo needs.
type scriptedTurns struct {
	turns [][]types.ModelChunk
	i     int
}

func (m *scriptedTurns) Profile() types.ModelProfile { return types.ModelProfile{Name: "scripted"} }

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

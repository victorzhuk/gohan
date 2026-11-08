package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/outputs"
)

// fixtureExtract is the CSV the scripted first turn writes.
const fixtureExtract = "id,region,revenue\n1,emea,1200.50\n2,apac,840.00\n3,amer,2210.75\n"

// piiValues are the clear values the fixture plants in the query stdout.
var piiValues = []string{
	fixtureSecretValue,
	"jordan.reed@example.com",
	"4111 1111 1111 1111",
}

func TestDataAnalystOffline(t *testing.T) {
	t.Run("sandbox.egress-denied", func(t *testing.T) {
		rep, _ := runComposed(t)
		spec, ok := specByName(rep.ToolSpecs, "run_code")
		if !ok {
			t.Fatalf("no run_code spec in %v", specNames(rep.ToolSpecs))
		}
		if spec.Effect != types.Idempotent {
			t.Errorf("run_code effect = %v, want the derived Idempotent", spec.Effect)
		}
		if spec.Network {
			t.Errorf("run_code spec carries a network capability")
		}
		// The offline fixture has no network layer, so the denial is a
		// policy fact on the specs, never an observed connection failure.
		for _, s := range rep.ToolSpecs {
			if s.Network {
				t.Errorf("%s spec carries a network capability", s.Name)
			}
			if s.Egress != nil && len(s.Egress.Allow) != 0 {
				t.Errorf("%s egress allow = %v, want the empty deny list", s.Name, s.Egress.Allow)
			}
		}
	})

	t.Run("sandbox.secret-injected", func(t *testing.T) {
		rep, jr := runComposed(t)
		calls := rep.Sandbox.setEnvCalls()
		// The per-session body injects on every executed sandbox call:
		// write and query in turn 1, the approved read in turn 2.
		if len(calls) != 3 {
			t.Fatalf("SetEnv calls = %d, want 3", len(calls))
		}
		for i, env := range calls {
			if len(env) != 1 || env["PGPASSWORD"] != fixtureSecretValue {
				t.Errorf("SetEnv %d = %v, want exactly {PGPASSWORD: <resolved value>}", i, env)
			}
		}
		events := eventsText(slices.Concat(rep.Turn1Events, rep.Turn2Events))
		history := messagesText(rep.History)
		// The journal of the composed run carries one record per model
		// turn; the store's record seam reads them as stored bytes.
		journal := journalText(t, jr, rep.SessionID)
		assertAbsent(t, "events", events, fixtureSecretValue)
		assertAbsent(t, "history", history, fixtureSecretValue)
		assertAbsent(t, "journal", journal, fixtureSecretValue)
	})

	t.Run("sandbox.secret-leak-redacted", func(t *testing.T) {
		rep, jr := runComposed(t)
		if stdout := string(rep.Sandbox.lastExec().Stdout); !strings.Contains(stdout, fixtureSecretValue) {
			t.Fatalf("scripted stdout does not print the injected value")
		}
		head := finishedText(rep.Turn1Events)
		if !strings.Contains(head, "<gohan:secret:") {
			t.Errorf("persisted result lacks the secret redaction marker")
		}
		body := storedBody(t, rep, rep.OutputRef)
		assertAbsent(t, "persisted result", head, fixtureSecretValue)
		assertAbsent(t, "stored body", body, fixtureSecretValue)
		assertAbsent(t, "history", messagesText(rep.History), fixtureSecretValue)
		assertAbsent(t, "journal", journalText(t, jr, rep.SessionID), fixtureSecretValue)
	})

	t.Run("sandbox.per-session-persists", func(t *testing.T) {
		rep, _ := runComposed(t)
		if got := rep.Sandbox.counters().Puts; got != 1 {
			t.Errorf("Puts = %d, want 1: turn 2 must read the persisted workspace without writing it again", got)
		}
		if len(rep.Sandbox.files()[extractPath]) == 0 {
			t.Errorf("extract missing from the persisted session workspace")
		}
		if turn2 := finishedText(rep.Turn2Events); !strings.Contains(turn2, "1,emea,1200.50") {
			t.Errorf("turn 2 result lacks the extract row: %q", turn2)
		}
	})

	t.Run("sandbox.snapshot-on-suspend", func(t *testing.T) {
		rep, _ := runComposed(t)
		if rep.WorkspaceRef == "" {
			t.Fatalf("suspension published no workspace ref")
		}
		refs := rep.Sandbox.snapshotRefs()
		idx := slices.Index(refs, rep.WorkspaceRef)
		if idx < 0 {
			t.Fatalf("published ref %s is not one the fake Snapshot minted (%d refs)", rep.WorkspaceRef, len(refs))
		}
		// Each publication mints one snapshot and bumps the generation by
		// one, so the mint order names the generation.
		if rep.WorkspaceGen != int64(idx+1) {
			t.Errorf("published generation = %d, want %d for ref %s", rep.WorkspaceGen, idx+1, rep.WorkspaceRef)
		}
	})

	t.Run("sandbox.resume-restores", func(t *testing.T) {
		rep, _ := runComposed(t)
		if got := rep.Sandbox.counters().Opens; got != 2 {
			t.Errorf("Opens = %d, want 2: one workspace per stack, none opened fresh from an empty ref", got)
		}
		if got := string(rep.Sandbox.files()[extractPath]); got != fixtureExtract {
			t.Errorf("resumed workspace extract = %q, want the turn-1 extract", got)
		}
	})

	t.Run("sandbox.replay-no-reexec", func(t *testing.T) {
		rep, _ := runComposed(t)
		if got := rep.Sandbox.counters().Execs; got != 1 {
			t.Errorf("Execs = %d, want 1: the replayed step must not execute again", got)
		}
		calls := rep.Sandbox.execCalls()
		if len(calls) != 1 {
			t.Fatalf("exec calls = %d, want 1", len(calls))
		}
		want := []string{"sh", "-c", "warehouse-report --top-customers"}
		if !slices.Equal(calls[0].Argv, want) {
			t.Errorf("exec argv = %v, want %v", calls[0].Argv, want)
		}
	})

	t.Run("sandbox.output-to-store", func(t *testing.T) {
		rep, _ := runComposed(t)
		if rep.OutputRef == "" {
			t.Fatalf("turn 1 stored no output ref")
		}
		if len(queryStdout()) <= outputs.DefaultMaxOutput {
			t.Fatalf("scripted stdout is not above the inline cap")
		}
		head := finishedText(rep.Turn1Events)
		if !strings.Contains(head, outputRefMarker+rep.OutputRef) {
			t.Errorf("head excerpt does not carry the stored ref %s", rep.OutputRef)
		}
		if !strings.Contains(head, fmt.Sprintf("stdout truncated at %d bytes", outputs.DefaultMaxOutput)) {
			t.Errorf("head excerpt does not carry the truncation marker")
		}
		if got := len(storedBody(t, rep, rep.OutputRef)); int64(got) <= outputs.DefaultMaxOutput {
			t.Errorf("store holds %d bytes, want the full body above the cap", got)
		}
	})

	t.Run("sandbox.output-stored-redacted", func(t *testing.T) {
		rep, _ := runComposed(t)
		body := storedBody(t, rep, rep.OutputRef)
		head := finishedText(rep.Turn1Events)
		for _, marker := range []string{"<gohan:secret:", "<gohan:email:", "<gohan:card:"} {
			if !strings.Contains(body, marker) {
				t.Errorf("stored body lacks the %q marker", marker)
			}
		}
		for _, v := range piiValues {
			assertAbsent(t, "stored body", body, v)
			assertAbsent(t, "head excerpt", head, v)
		}
	})

	t.Run("working-state.output-paging", func(t *testing.T) {
		rep, _ := runComposed(t)
		ctx := t.Context()
		body := storedBody(t, rep, rep.OutputRef)
		full, err := pageOutput(ctx, rep.Store, rep.ReadOutput, rep.OutputRef, &outputs.Span{Offset: 0, Limit: int64(len(body))})
		if err != nil {
			t.Fatalf("full page: %v", err)
		}
		if full != body {
			t.Errorf("full page = %d bytes, want the stored body of %d bytes", len(full), len(body))
		}
		window, err := pageOutput(ctx, rep.Store, rep.ReadOutput, rep.OutputRef, &outputs.Span{Offset: 40, Limit: 50})
		if err != nil {
			t.Fatalf("window page: %v", err)
		}
		if window != body[40:90] {
			t.Errorf("window page = %q, want body[40:90]", window)
		}
		if rep.Page != body[:pageLimit] {
			t.Errorf("composed page differs from the stored body prefix")
		}
		for _, span := range []*outputs.Span{
			{Offset: int64(len(body)), Limit: 1},
			{Offset: 0, Limit: int64(len(body)) + 1},
		} {
			if _, err := pageOutput(ctx, rep.Store, rep.ReadOutput, rep.OutputRef, span); err == nil {
				t.Errorf("window %+v was served, want a refusal", *span)
			}
		}
		if _, err := pageOutput(ctx, rep.Store, rep.ReadOutput, "no-such-output", &outputs.Span{Offset: 0, Limit: 16}); !errors.Is(err, stores.ErrOutputNotFound) {
			t.Errorf("unknown ref err = %v, want ErrOutputNotFound", err)
		}
	})

	t.Run("sandbox.data-analyst-resume-and-page", func(t *testing.T) {
		rep, _ := runComposed(t)
		c := rep.Sandbox.counters()
		if c.Puts != 1 {
			t.Errorf("Puts = %d, want 1: the resumed turn must not write again", c.Puts)
		}
		if c.Execs != 1 {
			t.Errorf("Execs = %d, want 1: the resumed turn must not re-execute the journaled step", c.Execs)
		}
		body := storedBody(t, rep, rep.OutputRef)
		for off := 0; off < len(body); off += pageLimit {
			end := min(off+pageLimit, len(body))
			page, err := pageOutput(t.Context(), rep.Store, rep.ReadOutput, rep.OutputRef, &outputs.Span{Offset: int64(off), Limit: int64(end - off)})
			if err != nil {
				t.Fatalf("page [%d,%d): %v", off, end, err)
			}
			for _, v := range piiValues {
				assertAbsent(t, fmt.Sprintf("page [%d,%d)", off, end), page, v)
			}
		}
		if !strings.Contains(rep.Page, "<gohan:") {
			t.Errorf("composed page carries no redaction marker")
		}
		for _, v := range piiValues {
			assertAbsent(t, "composed page", rep.Page, v)
		}
	})

	t.Run("fixture-rejects-escaping-paths", func(t *testing.T) {
		ctx := t.Context()
		sb := newFakeSandbox(t.TempDir())
		w, err := sb.Open(ctx, "", types.SandboxPolicy{})
		if err != nil {
			t.Fatalf("open workspace: %v", err)
		}
		for _, path := range []string{"../../etc/passwd", "/etc/passwd"} {
			if err := w.Put(ctx, path, strings.NewReader("root:x:0:0:root:/root:/bin/sh\n")); !errors.Is(err, errPathOutsideRoot) {
				t.Errorf("put %q err = %v, want it to wrap the root-escape refusal", path, err)
			}
		}
		if files := sb.files(); len(files) != 0 {
			t.Errorf("file map holds %d entries after refused writes, want none", len(files))
		}
		entries, err := os.ReadDir(sb.root)
		if err != nil {
			t.Fatalf("read temp root: %v", err)
		}
		if len(entries) != 0 {
			t.Errorf("temp root holds %d entries after refused writes, want none", len(entries))
		}
	})
}

// runComposed drives one fresh composition over its own temp root and
// fails the subtest when the composed run itself errors.
func runComposed(t *testing.T) (Report, *stores.MemoryJournal) {
	t.Helper()
	env := newAnalysisEnv(t.TempDir())
	rep, err := env.Analyze(t.Context())
	if err != nil {
		t.Fatalf("composed run: %v", err)
	}
	return rep, env.Journal
}

// assertAbsent fails the subtest when a clear value survives a surface.
func assertAbsent(t *testing.T, surface, text, value string) {
	t.Helper()
	if strings.Contains(text, value) {
		t.Errorf("%s carries the clear value %q", surface, value)
	}
}

// specByName finds one registered spec by tool name.
func specByName(specs []types.ToolSpec, name string) (types.ToolSpec, bool) {
	for _, spec := range specs {
		if spec.Name == name {
			return spec, true
		}
	}
	return types.ToolSpec{}, false
}

func specNames(specs []types.ToolSpec) []string {
	names := make([]string, 0, len(specs))
	for _, spec := range specs {
		names = append(names, spec.Name)
	}
	return names
}

// blockText flattens one block to the text an assertion can scan.
func blockText(b types.Block) string {
	switch v := b.(type) {
	case types.Text:
		return v.Text
	case types.Reasoning:
		return v.Text
	case types.ToolUse:
		return string(v.Args)
	case types.ToolResult:
		return blocksText(v.Content)
	case types.Document:
		return blocksText(v.Content)
	default:
		return ""
	}
}

func blocksText(blocks []types.Block) string {
	var sb strings.Builder
	for _, b := range blocks {
		sb.WriteString(blockText(b))
	}
	return sb.String()
}

func messagesText(messages []types.Message) string {
	var sb strings.Builder
	for _, msg := range messages {
		sb.WriteString(blocksText(msg.Blocks))
	}
	return sb.String()
}

// eventsText flattens the streamed turn events, the observable span
// surface this example records.
func eventsText(events []types.Event) string {
	var sb strings.Builder
	for _, ev := range events {
		switch v := ev.(type) {
		case types.TextDelta:
			sb.WriteString(v.Delta)
		case types.ReasoningDelta:
			sb.WriteString(v.Delta)
		case types.ResultDelta:
			sb.WriteString(v.Delta)
		case types.ToolArgsDelta:
			sb.WriteString(v.Delta)
		case types.ToolStarted:
			sb.WriteString(v.Call.Name)
			sb.WriteString(" ")
			sb.WriteString(string(v.Call.Args))
		case types.ToolFinished:
			sb.WriteString(blocksText(v.Result.Content))
		case types.AssistantMessage:
			sb.WriteString(blocksText(v.Message.Blocks))
		case types.GuardBlocked:
			sb.WriteString(v.Reason)
		}
	}
	return sb.String()
}

// finishedText flattens only the tool results the events carried.
func finishedText(events []types.Event) string {
	var sb strings.Builder
	for _, ev := range events {
		if fin, ok := ev.(types.ToolFinished); ok {
			sb.WriteString(blocksText(fin.Result.Content))
		}
	}
	return sb.String()
}

// journalRecords reads the journal records the composed run wrote, one
// per model turn, through the store's record seam as stored bytes. The
// model stage bumps the turn counter before it mints the key, so the
// three model steps are numbered from one.
func journalRecords(t *testing.T, j *stores.MemoryJournal, sessionID string) []string {
	t.Helper()
	out := make([]string, 0, 3)
	for turn := range 3 {
		data, err := j.StoredRecord(t.Context(), fmt.Sprintf("journal/%s/model/%d", sessionID, turn+1))
		if err != nil {
			t.Fatalf("read journal record for model turn %d: %v", turn+1, err)
		}
		out = append(out, string(data))
	}
	return out
}

func journalText(t *testing.T, j *stores.MemoryJournal, sessionID string) string {
	t.Helper()
	return strings.Join(journalRecords(t, j, sessionID), "\n")
}

// storedBody reads one stored ref and flattens it to the text body the
// paging helper sees.
func storedBody(t *testing.T, rep Report, ref string) string {
	t.Helper()
	data, err := rep.Store.Get(t.Context(), ref)
	if err != nil {
		t.Fatalf("read stored output %s: %v", ref, err)
	}
	var msg types.Message
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("decode stored output %s: %v", ref, err)
	}
	return blocksText(msg.Blocks)
}

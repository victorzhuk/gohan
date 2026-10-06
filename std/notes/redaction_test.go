package notes

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

type notesRedactor struct{}

func (notesRedactor) Redact(_ context.Context, blocks []types.Block, _ types.RedactionPolicy) ([]types.Block, types.RedactionMap, error) {
	out := append([]types.Block(nil), blocks...)
	entries := make(map[string]types.Entity)
	for i, block := range out {
		text, ok := block.(types.Text)
		if !ok {
			continue
		}
		if strings.Contains(text.Text, "anna@example.com") {
			text.Text = strings.ReplaceAll(text.Text, "anna@example.com", "[email:1]")
			out[i] = text
			entries["[email:1]"] = types.Entity{Kind: types.EntityEmail, Value: "anna@example.com"}
		}
	}
	return out, types.RedactionMap{Entries: entries}, nil
}

func (notesRedactor) Rehydrate(_ context.Context, blocks []types.Block, _ types.RedactionMap) ([]types.Block, error) {
	return blocks, nil
}

func TestNotesWriteRedactsBeforeStore(t *testing.T) {
	store := &stores.MemoryNotes{}
	maps := stores.NewMemoryRedactionMaps()
	n, err := New(store, WithRedaction(notesRedactor{}, types.RedactionPolicy{Detectors: []types.Detector{notesDetector{}}}, maps))
	if err != nil {
		t.Fatal(err)
	}
	info := runInfo("redacted-session")
	ctx := types.WithRunInfo(context.Background(), info)
	args, err := json.Marshal(writeArgs{Notes: "Contact anna@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := n.Call(ctx, args); err != nil {
		t.Fatal(err)
	}
	got, _, err := store.Read(ctx, sessionKey(info))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "anna@example.com") {
		t.Fatalf("stored notes contain raw entity: %q", got)
	}
	if !strings.Contains(got, "[email:1]") {
		t.Fatalf("stored notes = %q, want pseudonym", got)
	}
	if _, err := maps.Get(ctx, "acme", "redacted-session"); err != nil {
		t.Fatalf("redaction map: %v", err)
	}
}

type notesDetector struct{}

func (notesDetector) Detect(context.Context, []types.Block) ([]types.Span, error) { return nil, nil }

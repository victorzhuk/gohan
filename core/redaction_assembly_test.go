package gohan

import (
	"context"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

type assemblyRedactor struct{}

func (assemblyRedactor) Redact(_ context.Context, blocks []types.Block, _ types.RedactionPolicy) ([]types.Block, types.RedactionMap, error) {
	out := append([]types.Block(nil), blocks...)
	entries := make(map[string]types.Entity)
	for i, block := range out {
		text, ok := block.(types.Text)
		if !ok || !strings.Contains(text.Text, "Ada Lovelace") {
			continue
		}
		text.Text = strings.ReplaceAll(text.Text, "Ada Lovelace", "[person]")
		out[i] = text
		entries["[person]"] = types.Entity{Kind: types.EntityPerson, Value: "Ada Lovelace"}
	}
	return out, types.RedactionMap{Entries: entries}, nil
}

func (assemblyRedactor) Rehydrate(context.Context, []types.Block, types.RedactionMap) ([]types.Block, error) {
	return nil, nil
}

func TestNativeAssembleRedactsAndPersistsMap(t *testing.T) {
	mapStore := stores.NewMemoryRedactionMaps()
	assemble := nativeAssemble(chains.PromptSet{}, func(context.Context, types.AssembleInput) (types.ModelRequest, error) {
		return types.ModelRequest{Messages: []types.Message{{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "Book for Ada Lovelace"}}}}}, nil
	}, assemblyRedactor{}, types.RedactionPolicy{Detectors: []types.Detector{nil}}, mapStore, blobAssembly{})
	run := types.RunInfo{SessionID: "session", Principal: types.Principal{Tenant: "tenant"}}
	req, err := assemble(context.Background(), types.AssembleInput{Run: run})
	if err != nil {
		t.Fatal(err)
	}
	got := req.Messages[0].Blocks[0].(types.Text).Text
	if strings.Contains(got, "Ada Lovelace") || !strings.Contains(got, "[person]") {
		t.Fatalf("model request text = %q", got)
	}
	m, err := mapStore.Get(context.Background(), "tenant", "session")
	if err != nil {
		t.Fatal(err)
	}
	if m.Entries["[person]"].Value != "Ada Lovelace" {
		t.Fatalf("map entries = %+v", m.Entries)
	}
}

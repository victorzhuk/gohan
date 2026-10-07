package gohan

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/types"
)

func TestNativeAssembleRefusesMissingAssembler(t *testing.T) {
	_, err := nativeAssemble("chat", chains.PromptSet{}, nil, nil, types.RedactionPolicy{}, nil, blobAssembly{})(context.Background(), types.AssembleInput{})
	if !errors.Is(err, errNoAssembler) {
		t.Fatalf("assemble without assembler: %v, want errNoAssembler", err)
	}
	if !strings.Contains(err.Error(), "chat") {
		t.Errorf("error %q does not name the flow", err)
	}
}

func TestNativeAssemblePassesAssemblerThrough(t *testing.T) {
	assemble := nativeAssemble("chat", chains.PromptSet{}, func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
		return types.ModelRequest{Messages: append(append([]types.Message{}, in.History...), in.Input...)}, nil
	}, nil, types.RedactionPolicy{}, nil, blobAssembly{})
	in := types.AssembleInput{History: []types.Message{{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "hi"}}}}}
	req, err := assemble(context.Background(), in)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if len(req.Messages) != 1 {
		t.Fatalf("messages = %d, want the assembled history", len(req.Messages))
	}
}

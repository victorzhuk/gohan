package gohan

import (
	"context"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

type redactorProbe struct{}

func (redactorProbe) Redact(context.Context, []types.Block, types.RedactionPolicy) ([]types.Block, types.RedactionMap, error) {
	return nil, types.RedactionMap{}, nil
}

func (redactorProbe) Rehydrate(context.Context, []types.Block, types.RedactionMap) ([]types.Block, error) {
	return nil, nil
}

func TestWithRedactorOption(t *testing.T) {
	probe := redactorProbe{}
	var cfg config
	if err := WithRedactor(probe)(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.redactor != probe {
		t.Fatal("redactor option did not set configuration")
	}
}

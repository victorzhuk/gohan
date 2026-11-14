package gohan

import (
	"context"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

func TestRunIdentityDepth(t *testing.T) {
	tests := []struct {
		name           string
		ctx            func() context.Context
		wantRoot       string
		wantParent     string
		wantDepth      int
		wantCostTagFtr string
	}{
		{
			name:     "no ambient run info roots its own tree",
			ctx:      context.Background,
			wantRoot: "run-child",
		},
		{
			name: "same run inherits tree identity from caller",
			ctx: func() context.Context {
				return types.WithRunInfo(context.Background(), types.RunInfo{
					RunID:       "run-child",
					RootRunID:   "run-root",
					ParentRunID: "run-parent",
					Depth:       2,
					CostTags:    types.CostTags{Feature: "f"},
				})
			},
			wantRoot:       "run-root",
			wantParent:     "run-parent",
			wantDepth:      2,
			wantCostTagFtr: "f",
		},
		{
			name: "different run id keeps root identity, carries tags",
			ctx: func() context.Context {
				return types.WithRunInfo(context.Background(), types.RunInfo{
					RunID:    "run-other",
					CostTags: types.CostTags{Feature: "f"},
				})
			},
			wantRoot:       "run-child",
			wantCostTagFtr: "f",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, _ := types.RunInfoFrom((&conversation{spec: "child"}).runIdentityCtx(tt.ctx(), "run-child", "s1"))
			if info.RootRunID != tt.wantRoot {
				t.Fatalf("RootRunID = %q, want %q", info.RootRunID, tt.wantRoot)
			}
			if info.ParentRunID != tt.wantParent {
				t.Fatalf("ParentRunID = %q, want %q", info.ParentRunID, tt.wantParent)
			}
			if info.Depth != tt.wantDepth {
				t.Fatalf("Depth = %d, want %d", info.Depth, tt.wantDepth)
			}
			if got := info.CostTags.Feature; got != tt.wantCostTagFtr {
				t.Fatalf("CostTags.Feature = %q, want %q", got, tt.wantCostTagFtr)
			}
		})
	}
}

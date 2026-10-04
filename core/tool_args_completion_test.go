package gohan

import (
	"encoding/json/jsontext"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

func TestToolArgsCompletion(t *testing.T) {
	t.Run("complete-args-pass", func(t *testing.T) {
		cu := types.ToolUse{ID: "c1", Name: "record", Args: jsontext.Value(`{"a":1}`)}
		if _, ok := validateCompletion(cu); !ok {
			t.Fatalf("complete arguments refused: %v", cu.Args)
		}
	})

	t.Run("duplicate-key-refused-at-completion", func(t *testing.T) {
		cu := types.ToolUse{ID: "c1", Name: "record", Args: jsontext.Value(`{"a":1,"a":2}`)}
		res, ok := validateCompletion(cu)
		if ok {
			t.Fatal("duplicate key accepted")
		}
		if res.ID != "c1" || res.Outcome != types.Failed || res.Error == nil || res.Error.Kind != types.Permanent {
			t.Fatalf("result = %+v, want Failed(Permanent) for c1", res)
		}
	})

	t.Run("truncated-args-refused", func(t *testing.T) {
		cu := types.ToolUse{ID: "c1", Name: "record", Args: jsontext.Value(`{"a":`)}
		res, ok := validateCompletion(cu)
		if ok {
			t.Fatal("truncated arguments accepted")
		}
		if res.Outcome != types.Failed {
			t.Fatalf("result = %+v, want Failed", res)
		}
	})
}

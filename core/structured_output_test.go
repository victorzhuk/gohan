package gohan

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

// fiveDeepSchema nests five object levels, one past the depth-4 strict
// bound (structured-output.unrepresentable-schema).
const fiveDeepSchema = `{
  "type": "object",
  "properties": {
    "a": {
      "type": "object",
      "properties": {
        "b": {
          "type": "object",
          "properties": {
            "c": {
              "type": "object",
              "properties": {
                "d": {
                  "type": "object",
                  "properties": {"e": {"type": "string"}},
                  "required": ["e"],
                  "additionalProperties": false
                }
              },
              "required": ["d"],
              "additionalProperties": false
            }
          },
          "required": ["c"],
          "additionalProperties": false
        }
      },
      "required": ["b"],
      "additionalProperties": false
    }
  },
  "required": ["a"],
  "additionalProperties": false
}`

func TestStructuredOutputHardening(t *testing.T) {
	t.Run("structured-output.cap-negotiation", func(t *testing.T) {
		s, err := Build(
			WithModels(
				gohantest.NewScriptedModel(profile("capped", true), gohantest.Text("ok")),
				gohantest.NewScriptedModel(profile("plain", false), gohantest.Text("ok")),
			),
			WithNativeAgent(NativeSpec{Request: FlowRequest{Name: "a-plain-flow", Structured: true}, Profile: "plain"}),
			WithNativeAgent(NativeSpec{Request: FlowRequest{Name: "z-capped-flow", Structured: true}, Profile: "capped"}),
		)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		profiles := map[string]types.ModelProfile{
			"capped": profile("capped", true),
			"plain":  profile("plain", false),
		}
		plan, err := s.ResolveStrategies(FlowRequest{Name: "f", Structured: true}, profiles, "capped")
		if err != nil {
			t.Fatalf("ResolveStrategies on capped profile: %v", err)
		}
		if plan.Structured != StructuredConstrained {
			t.Errorf("capped profile resolved %v, want constrained", plan.Structured)
		}
		plan, err = s.ResolveStrategies(FlowRequest{Name: "f", Structured: true}, profiles, "plain")
		if err != nil {
			t.Fatalf("ResolveStrategies on uncapped profile: %v", err)
		}
		if plan.Structured != StructuredToolSchema {
			t.Errorf("uncapped profile resolved %v, want tool schema", plan.Structured)
		}
		if _, err := s.ResolveStrategies(FlowRequest{Name: "f", Strategy: StructuredConstrained}, profiles, "plain"); !errors.Is(err, ErrStrategyUnsupported) {
			t.Fatalf("err = %v, want ErrStrategyUnsupported for explicit constrained on the uncapped profile", err)
		}
		var gotPlain, gotCapped bool
		for _, e := range nativeMatrixEntries(s.native) {
			switch e.Flow {
			case "a-plain-flow":
				gotPlain = true
				if e.Structured != "tool_schema" || e.Profile != "plain" {
					t.Errorf("matrix entry %+v, want plain/tool_schema", e)
				}
			case "z-capped-flow":
				gotCapped = true
				if e.Structured != "constrained" || e.Profile != "capped" {
					t.Errorf("matrix entry %+v, want capped/constrained", e)
				}
			}
		}
		if !gotPlain || !gotCapped {
			t.Fatalf("resolved matrix missing entries: plain=%t capped=%t", gotPlain, gotCapped)
		}
	})

	t.Run("structured-output.unrepresentable-schema", func(t *testing.T) {
		_, err := Build(
			WithModels(gohantest.NewScriptedModel(profile("capped", true), gohantest.Text("ok"))),
			WithNativeAgent(NativeSpec{
				Request:      FlowRequest{Name: "extract", Structured: true},
				Profile:      "capped",
				ModelOptions: types.ModelOptions{ResponseSchema: json.RawMessage(fiveDeepSchema)},
			}),
		)
		if err == nil {
			t.Fatal("Build accepted a schema nested 5 deep under Constrained")
		}
		for _, part := range []string{"extract", "properties.a.properties.b.properties.c.properties.d", "maximum depth 4"} {
			if !strings.Contains(err.Error(), part) {
				t.Errorf("err %q misses %q", err, part)
			}
		}
	})
}

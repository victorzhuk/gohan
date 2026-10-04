package structured

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

type invoiceLine struct {
	SKU string `json:"sku"`
	Qty int    `json:"qty"`
}

type invoice struct {
	ID     string        `json:"id"`
	Total  float64       `json:"total" min:"0"`
	Lines  []invoiceLine `json:"lines"`
	UserID string        `json:"-"` // excluded: identity is never set by the model
}

func decodeSchema(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	return schema
}

func TestStructuredStrategy(t *testing.T) {
	t.Run("structured-output.strict-schema", func(t *testing.T) {
		raw, err := NewToolSchema(true).Schema(reflect.TypeFor[invoice]())
		if err != nil {
			t.Fatalf("schema: %v", err)
		}
		schema := decodeSchema(t, raw)
		if v, ok := schema["additionalProperties"].(bool); !ok || v {
			t.Errorf("additionalProperties = %v, want false", schema["additionalProperties"])
		}
		if schema["strict"] != true {
			t.Errorf("strict = %v, want true", schema["strict"])
		}
		required, _ := schema["required"].([]any)
		var names []string
		for _, r := range required {
			if s, ok := r.(string); ok {
				names = append(names, s)
			}
		}
		for _, want := range []string{"id", "total", "lines"} {
			found := false
			for _, n := range names {
				if n == want {
					found = true
				}
			}
			if !found {
				t.Errorf("required missing %q (got %v)", want, names)
			}
		}
		props, _ := schema["properties"].(map[string]any)
		if _, ok := props["user_id"]; ok {
			t.Error("excluded identity field leaked into the schema")
		}
	})

	t.Run("derivation from struct tags produces a strict schema", func(t *testing.T) {
		type out struct {
			Name string `json:"name"`
		}
		raw, err := NewToolSchema(true).Schema(reflect.TypeFor[out]())
		if err != nil {
			t.Fatalf("schema: %v", err)
		}
		schema := decodeSchema(t, raw)
		props, _ := schema["properties"].(map[string]any)
		if _, ok := props["name"]; !ok {
			t.Errorf("properties missing tagged field %q (got %v)", "name", props)
		}
	})

	t.Run("unsupported shape is rejected with the right error", func(t *testing.T) {
		if _, err := NewToolSchema(true).Schema(reflect.TypeFor[string]()); !errors.Is(err, types.ErrStructuredOutput) {
			t.Errorf("non-struct In: err = %v, want ErrStructuredOutput", err)
		}
		type out struct {
			Any any `json:"any"`
		}
		if _, err := NewToolSchema(true).Schema(reflect.TypeFor[out]()); !errors.Is(err, types.ErrStructuredOutput) {
			t.Errorf("untyped field: err = %v, want ErrStructuredOutput", err)
		}
	})

	t.Run("structured-output.reason-first", func(t *testing.T) {
		schema := json.RawMessage(`{"type":"object"}`)
		opts := types.ModelOptions{ResponseSchema: schema}
		got, ok := NewReasonFirst().Options(opts)
		if !ok {
			t.Fatal("Options reported no reorder for a constrained request")
		}
		if len(got.ResponseSchema) != 0 {
			t.Errorf("ResponseSchema = %s, want empty: the constraint must not apply to the reasoning phase", got.ResponseSchema)
		}
		if string(got.Extra[reasonFirstSlot].(json.RawMessage)) != string(schema) {
			t.Errorf("reason-first slot = %v, want the constrained schema", got.Extra[reasonFirstSlot])
		}
	})

	t.Run("reason-first options request the reasoning slot first", func(t *testing.T) {
		opts := types.ModelOptions{}
		got, ok := NewReasonFirst().Options(opts)
		if ok {
			t.Error("Options reordered a request without a response schema")
		}
		if _, has := got.Extra[reasonFirstSlot]; has {
			t.Error("reason-first slot set without a constrained schema")
		}
		if NewReasonFirst().Explain() != "ReasonFirst: on" {
			t.Errorf("Explain = %q, want %q", NewReasonFirst().Explain(), "ReasonFirst: on")
		}
	})
}

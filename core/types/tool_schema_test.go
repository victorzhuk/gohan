package types

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func mustSchema(t *testing.T, b *SchemaBuilder, in any) map[string]any {
	t.Helper()
	raw, err := b.Schema(reflect.TypeOf(in))
	if err != nil {
		t.Fatalf("Schema: unexpected error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
	return m
}

func TestToolSchema(t *testing.T) {
	t.Run("tools.schema-from-tags", func(t *testing.T) {
		type booking struct {
			Currency string `json:"currency" desc:"ISO code" enum:"EUR,USD"`
			Nights   int    `json:"nights,omitempty" min:"1" max:"30"`
		}
		m := mustSchema(t, NewSchemaBuilder(), booking{})
		if m["additionalProperties"] != false {
			t.Errorf("additionalProperties = %v, want false", m["additionalProperties"])
		}
		props := m["properties"].(map[string]any)
		currency := props["currency"].(map[string]any)
		if currency["type"] != "string" || currency["description"] != "ISO code" {
			t.Errorf("currency = %v, want string type with ISO code description", currency)
		}
		if want := []any{"EUR", "USD"}; !reflect.DeepEqual(currency["enum"], want) {
			t.Errorf("currency enum = %v, want %v", currency["enum"], want)
		}
		nights := props["nights"].(map[string]any)
		if nights["minimum"] != float64(1) || nights["maximum"] != float64(30) {
			t.Errorf("nights bounds = %v, want min 1 max 30", nights)
		}
		required := m["required"].([]any)
		if want := []any{"currency"}; !reflect.DeepEqual(required, want) {
			t.Errorf("required = %v, want %v", required, want)
		}

		// Optional field: omitempty keeps the name out of required.
		type note struct {
			Body string `json:"body,omitempty" desc:"note body"`
		}
		m = mustSchema(t, NewSchemaBuilder(), note{})
		if req := m["required"].([]any); len(req) != 0 {
			t.Errorf("required = %v, want empty", req)
		}

		// Identity fields are excluded from the schema: the runtime injects
		// them, the model never sets them.
		b := NewSchemaBuilder(WithIdentityFields(matchList{"user_id", "tenant"}))
		type args struct {
			UserID string `json:"user_id"`
			Tenant string `json:"tenant"`
			Query  string `json:"query"`
		}
		m = mustSchema(t, b, args{})
		props = m["properties"].(map[string]any)
		if _, ok := props["user_id"]; ok {
			t.Error("user_id present in schema, want excluded")
		}
		if _, ok := props["tenant"]; ok {
			t.Error("tenant present in schema, want excluded")
		}
		if _, ok := props["query"]; !ok {
			t.Error("query missing from schema")
		}

		// Nested structs, slices and maps are allowed to depth 4.
		type l4 struct {
			Tag string `json:"tag"`
		}
		type l3 struct {
			Items []l4 `json:"items"`
		}
		type l2 struct {
			Level l3 `json:"level"`
		}
		type l1 struct {
			Nested l2                `json:"nested"`
			Labels map[string]string `json:"labels"`
		}
		mustSchema(t, NewSchemaBuilder(), l1{})

		// Depth 5 is rejected.
		type d5 struct {
			V string `json:"v"`
		}
		type d4 struct {
			Nested d5 `json:"nested"`
		}
		type d3 struct {
			Nested d4 `json:"nested"`
		}
		type d2 struct {
			Nested d3 `json:"nested"`
		}
		type d1 struct {
			Nested d2 `json:"nested"`
		}
		if _, err := NewSchemaBuilder().Schema(reflect.TypeOf(d1{})); err == nil {
			t.Error("depth-5 In accepted, want rejection")
		}
	})

	t.Run("tools.untyped-args-rejected", func(t *testing.T) {
		type loose struct {
			Filter map[string]any `json:"filter"`
		}
		_, err := NewSchemaBuilder().Schema(reflect.TypeOf(loose{}))
		if err == nil {
			t.Fatal("map[string]any accepted without WithRawArgs, want rejection")
		}
		if !strings.Contains(err.Error(), `"filter"`) {
			t.Errorf("error %q does not name the field", err)
		}

		type anyField struct {
			Payload any `json:"payload"`
		}
		if _, err := NewSchemaBuilder().Schema(reflect.TypeOf(anyField{})); err == nil {
			t.Error("any accepted without WithRawArgs, want rejection")
		}

		type rawField struct {
			Payload json.RawMessage `json:"payload"`
		}
		if _, err := NewSchemaBuilder().Schema(reflect.TypeOf(rawField{})); err == nil {
			t.Error("json.RawMessage accepted without WithRawArgs, want rejection")
		}

		// WithRawArgs allows all three.
		b := NewSchemaBuilder(WithRawArgs())
		for _, in := range []any{loose{}, anyField{}, rawField{}} {
			if _, err := b.Schema(reflect.TypeOf(in)); err != nil {
				t.Errorf("%T: %v", in, err)
			}
		}
	})
}

// matchList is a test stand-in for the driver's *IdentityFieldMatcher.
type matchList []string

func (m matchList) Match(name string) bool {
	for _, n := range m {
		if n == name {
			return true
		}
	}
	return false
}

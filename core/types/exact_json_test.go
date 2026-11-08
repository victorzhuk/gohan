package types

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestCanonicalJSONExactValues(t *testing.T) {
	t.Run("integer spellings normalize", func(t *testing.T) {
		for _, spelling := range []string{"1", "1.0", "1e0", "1E0"} {
			got, err := CanonicalJSONValue(json.RawMessage(`{"n":` + spelling + `}`))
			if err != nil {
				t.Fatalf("canonicalize %s: %v", spelling, err)
			}
			if string(got) != `{"n":1}` {
				t.Fatalf("canonicalize %s = %s, want the integer form", spelling, got)
			}
		}
	})

	t.Run("adjacent integers stay distinct", func(t *testing.T) {
		a, err := CanonicalJSONValue(json.RawMessage(`{"n":9007199254740992}`))
		if err != nil {
			t.Fatal(err)
		}
		b, err := CanonicalJSONValue(json.RawMessage(`{"n":9007199254740993}`))
		if err != nil {
			t.Fatal(err)
		}
		if string(a) != `{"n":9007199254740992}` || string(b) != `{"n":9007199254740993}` {
			t.Fatalf("canonical form = %s, %s, want the exact integer digits", a, b)
		}
	})

	t.Run("signed zero normalizes", func(t *testing.T) {
		got, err := CanonicalJSONValue(json.RawMessage(`[-0, 0.0, -0e5]`))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != `[0,0,0]` {
			t.Fatalf("canonical form = %s, want zero without a sign", got)
		}
	})

	t.Run("decimals expand exactly", func(t *testing.T) {
		for _, tc := range []struct{ in, want string }{
			{`2.25`, `2.25`},
			{`0.1`, `0.1`},
			{`1.50`, `1.5`},
			{`-1.5`, `-1.5`},
			{`0.123456789012345678901234567890`, `0.12345678901234567890123456789`},
		} {
			got, err := CanonicalJSONValue(json.RawMessage(`{"n":` + tc.in + `}`))
			if err != nil {
				t.Fatalf("canonicalize %s: %v", tc.in, err)
			}
			if string(got) != `{"n":`+tc.want+`}` {
				t.Fatalf("canonicalize %s = %s, want %s", tc.in, got, tc.want)
			}
		}
	})

	t.Run("exponents expand exactly", func(t *testing.T) {
		for _, tc := range []struct{ in, want string }{
			{`1e2`, `100`},
			{`2.5e1`, `25`},
			{`1e-7`, `0.0000001`},
			{`1e21`, `1000000000000000000000`},
		} {
			got, err := CanonicalJSONValue(json.RawMessage(`{"n":` + tc.in + `}`))
			if err != nil {
				t.Fatalf("canonicalize %s: %v", tc.in, err)
			}
			if string(got) != `{"n":`+tc.want+`}` {
				t.Fatalf("canonicalize %s = %s, want %s", tc.in, got, tc.want)
			}
		}
	})

	t.Run("nested values and member order preserve identity", func(t *testing.T) {
		first := `{"a":{"n":9007199254740992,"deep":[1,{"z":1,"a":2}]},"b":null}`
		second := `{"b":null,"a":{"deep":[1,{"a":2,"z":1}],"n":9007199254740992}}`
		a, err := CanonicalJSONValue(json.RawMessage(first))
		if err != nil {
			t.Fatal(err)
		}
		b, err := CanonicalJSONValue(json.RawMessage(second))
		if err != nil {
			t.Fatal(err)
		}
		if string(a) != string(b) {
			t.Fatalf("equivalent nested values diverged: %s vs %s", a, b)
		}
		if !strings.Contains(string(a), "9007199254740992") {
			t.Fatalf("canonical form %s lost the exact nested integer", a)
		}
	})

	t.Run("strings pass through", func(t *testing.T) {
		a, err := CanonicalJSONValue(json.RawMessage(`{"s":"a\nbé","t":"\u0041"}`))
		if err != nil {
			t.Fatal(err)
		}
		b, err := CanonicalJSONValue(json.RawMessage(`{"t":"A","s":"a\nbé"}`))
		if err != nil {
			t.Fatal(err)
		}
		if string(a) != string(b) {
			t.Fatalf("equivalent strings diverged: %s vs %s", a, b)
		}
	})

	t.Run("invalid input is refused", func(t *testing.T) {
		for _, tc := range []struct{ name, in, reason string }{
			{"syntax", `{"a":}`, "syntax"},
			{"duplicate key", `{"a":1,"a":2}`, "duplicate_key"},
			{"trailing value", `{"a":1} {"b":2}`, "syntax"},
			{"empty", ``, "syntax"},
			{"invalid utf8", "{\"a\":\"\xff\"}", "invalid_utf8"},
		} {
			_, err := CanonicalJSONValue(json.RawMessage(tc.in))
			var argsErr *ToolArgsError
			if err == nil || !errors.As(err, &argsErr) || argsErr.Reason != tc.reason {
				t.Fatalf("%s: err = %v, want a ToolArgsError with reason %q", tc.name, err, tc.reason)
			}
		}
	})
}

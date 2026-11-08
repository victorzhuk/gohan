package types

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"io"
	"math/big"
	"unicode/utf8"
)

// ExactFingerprintVersion prefixes approval and journal fingerprints minted
// under the exact numeric identity format, so a stored legacy rounded
// fingerprint can never match a fresh one.
const ExactFingerprintVersion = "exact-v1:"

// CanonicalJSONValue renders one JSON value in the exact canonical form:
// object members sorted, whitespace collapsed, and every number rendered by
// its exact decimal value without floating-point conversion. Equivalent
// decimal spellings of one value render identically, so 1e2 and 100
// canonicalize to the same bytes.
func CanonicalJSONValue(args json.RawMessage) (json.RawMessage, error) {
	if err := validateExactJSONText(args); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, syntaxError(err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, &ToolArgsError{Reason: "syntax", Message: "tool arguments hold more than one JSON value"}
	}
	canonical, err := exactNumbers(v)
	if err != nil {
		return nil, err
	}
	out, err := json.Marshal(canonical)
	if err != nil {
		return nil, &ToolArgsError{Reason: "syntax", Message: "tool arguments cannot be canonicalized: " + err.Error()}
	}
	return out, nil
}

func validateExactJSONText(args json.RawMessage) error {
	if !utf8.Valid(args) {
		return &ToolArgsError{Reason: "invalid_utf8", Message: "tool arguments are not valid UTF-8"}
	}
	d := jsontext.NewDecoder(bytes.NewReader(args))
	if _, err := d.ReadValue(); err != nil {
		if errors.Is(err, jsontext.ErrDuplicateName) {
			return &ToolArgsError{Reason: "duplicate_key", Message: "tool arguments contain a duplicate key"}
		}
		return &ToolArgsError{Reason: "syntax", Message: "tool arguments are not valid JSON: " + err.Error()}
	}
	return nil
}

func syntaxError(err error) error {
	return &ToolArgsError{Reason: "syntax", Message: "tool arguments are not valid JSON: " + err.Error()}
}

// exactNumbers rebuilds the decoded value, replacing every JSON number with
// its exact canonical digits as raw JSON, so re-encoding never routes a
// number through float64.
func exactNumbers(v any) (any, error) {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			ne, err := exactNumbers(e)
			if err != nil {
				return nil, err
			}
			out[k] = ne
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			ne, err := exactNumbers(e)
			if err != nil {
				return nil, err
			}
			out[i] = ne
		}
		return out, nil
	case json.Number:
		digits, err := canonicalNumber(t)
		if err != nil {
			return nil, err
		}
		return json.RawMessage(digits), nil
	default:
		return v, nil
	}
}

// canonicalNumber renders the number's exact decimal value: an integer for
// whole values, otherwise the minimal digits past the decimal point. A JSON
// number reduces to a terminating decimal, so the digit expansion ends.
func canonicalNumber(n json.Number) (string, error) {
	r, ok := new(big.Rat).SetString(n.String())
	if !ok {
		return "", &ToolArgsError{Reason: "syntax", Message: "tool arguments carry an invalid JSON number " + n.String()}
	}
	negative := r.Sign() < 0
	den := r.Denom()
	q, rem := new(big.Int).QuoRem(new(big.Int).Abs(r.Num()), den, new(big.Int))
	digits := q.String()
	if rem.Sign() != 0 {
		digits += "."
		ten := big.NewInt(10)
		for rem.Sign() != 0 {
			rem.Mul(rem, ten)
			d, m := new(big.Int).QuoRem(rem, den, new(big.Int))
			digits += d.String()
			rem = m
		}
	}
	if negative {
		return "-" + digits, nil
	}
	return digits, nil
}

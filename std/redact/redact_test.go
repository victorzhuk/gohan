package redact

import (
	"context"
	"errors"
	"testing"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

func TestRedactRehydrateAndStoreRoundTrip(t *testing.T) {
	ctx := types.WithRunInfo(context.Background(), types.RunInfo{SessionID: "s1", Principal: types.Principal{Tenant: "t1"}})
	r := New(KeyFunc(func(context.Context, string) ([]byte, error) { return []byte("fixed-key"), nil }))
	input := []types.Block{types.Text{Text: "Contact anna@example.com"}}
	redacted, mapping, err := r.Redact(ctx, input, types.RedactionPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	masked := redacted[0].(types.Text).Text
	if masked == input[0].(types.Text).Text || mapping.Tenant != "t1" || mapping.Session != "s1" {
		t.Fatalf("redacted=%q map=%+v", masked, mapping)
	}
	store := stores.NewMemoryRedactionMaps()
	if err := store.Put(ctx, "t1", "s1", mapping); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Get(ctx, "t1", "s1")
	if err != nil {
		t.Fatal(err)
	}
	restored, err := r.Rehydrate(ctx, redacted, loaded)
	if err != nil {
		t.Fatal(err)
	}
	if got := restored[0].(types.Text).Text; got != input[0].(types.Text).Text {
		t.Fatalf("rehydrated = %q", got)
	}
}

func TestRulesAndValidation(t *testing.T) {
	if err := ValidateRules(); err != nil {
		t.Fatal(err)
	}
	if !luhn("4532015112830366") || luhn("4532015112830367") {
		t.Fatal("card checksum validation failed")
	}
	if !iban("GB82 WEST 1234 5698 7654 32") || iban("GB82 WEST 1234 5698 7654 33") {
		t.Fatal("IBAN checksum validation failed")
	}
}

func TestUnsupportedMode(t *testing.T) {
	r := New(KeyFunc(func(context.Context, string) ([]byte, error) { return []byte("key"), nil }))
	_, _, err := r.Redact(context.Background(), nil, types.RedactionPolicy{Mode: types.Vaulted})
	if !errors.Is(err, ErrModeUnsupported) {
		t.Fatalf("error = %v", err)
	}
}

package types

import (
	"context"
	"reflect"
	"testing"
)

func TestTaintWindow(t *testing.T) {
	blocks := []Block{
		Text{BlockBase: BlockBase{Origin: Origin{Kind: OriginTool, Name: "fetch"}}, Text: "untrusted content"},
	}
	ctx := WithTaintWindow(t.Context(), blocks)
	got, ok := TaintWindowFrom(ctx)
	if !ok {
		t.Fatal("TaintWindowFrom returned no window")
	}
	if !reflect.DeepEqual(got, blocks) {
		t.Fatalf("TaintWindowFrom() = %#v, want %#v", got, blocks)
	}

	if _, ok := TaintWindowFrom(context.Background()); ok {
		t.Fatal("TaintWindowFrom returned a window for a bare context")
	}
}

func TestErrTaintTrifecta(t *testing.T) {
	err := ErrTaintTrifecta{
		Flow:        "customer-support",
		Exfil:       true,
		PrivateRead: false,
		Untrusted:   true,
	}
	want := "gohan: flow \"customer-support\" requires a taint policy (exfil=true, private_read=false, untrusted=true)"
	if got := err.Error(); got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

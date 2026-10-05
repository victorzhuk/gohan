package gohan

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

func TestClassifyFailureEgress(t *testing.T) {
	denied := types.EgressDenied{URL: "https://example.com", Reason: "private range"}
	result := classifyFailure(context.Background(), types.ToolSpec{Name: "egress", Effect: types.ReadOnly}, denied)
	if result.Outcome != types.Failed || result.Error.Kind != types.Permanent || result.Error.Message != denied.Error() {
		t.Fatalf("egress denial result = %+v", result)
	}
	unrelated := classifyFailure(context.Background(), types.ToolSpec{Name: "egress", Effect: types.ReadOnly}, errors.New("other failure"))
	if unrelated.Outcome != types.Failed || unrelated.Error.Kind != types.Permanent {
		t.Fatalf("unrelated failure result = %+v", unrelated)
	}
}

func TestAuditLogFrom(t *testing.T) {
	if _, ok := AuditLogFrom(context.Background()); ok {
		t.Fatal("unbound audit log was present")
	}
	audit := stores.NewMemoryAuditLog()
	got, ok := AuditLogFrom(WithDecisionAudit(context.Background(), audit))
	if !ok || got != audit {
		t.Fatalf("bound audit log = (%v, %t), want (%p, true)", got, ok, audit)
	}
}

func TestToolEgressDeclarations(t *testing.T) {
	allow := []string{"example.com"}
	schemes := []string{"https"}
	tool, err := NewTool[struct{}, struct{}]("egress", "", func(context.Context, struct{}) (struct{}, error) {
		return struct{}{}, nil
	}, WithEgress(types.EgressPolicy{Allow: allow, Schemes: schemes}))
	if err != nil {
		t.Fatal(err)
	}
	allow[0] = "changed.example"
	schemes[0] = "http"
	spec := tool.Spec()
	if spec.Egress.Allow[0] != "example.com" || spec.Egress.Schemes[0] != "https" {
		t.Fatalf("policy slices were not copied: %+v", spec.Egress)
	}

	networkTool, err := NewTool[struct{}, struct{}]("http", "", func(context.Context, struct{}) (struct{}, error) {
		return struct{}{}, nil
	}, WithHTTP())
	if err != nil {
		t.Fatal(err)
	}
	if !networkTool.Spec().Network || networkTool.Spec().Capabilities.Exfil {
		t.Fatalf("WithHTTP declaration = %+v", networkTool.Spec())
	}

	exfilTool, err := NewTool[struct{}, struct{}]("exfil", "", func(context.Context, struct{}) (struct{}, error) {
		return struct{}{}, nil
	}, WithExfil())
	if err != nil {
		t.Fatal(err)
	}
	if !exfilTool.Spec().Network || !exfilTool.Spec().Capabilities.Exfil {
		t.Fatalf("WithExfil declaration = %+v", exfilTool.Spec())
	}

	privateReadTool, err := NewTool[struct{}, struct{}]("private_read", "", func(context.Context, struct{}) (struct{}, error) {
		return struct{}{}, nil
	}, WithPrivateRead())
	if err != nil {
		t.Fatal(err)
	}
	if privateReadTool.Spec().Network || !privateReadTool.Spec().Capabilities.PrivateRead {
		t.Fatalf("WithPrivateRead declaration = %+v", privateReadTool.Spec())
	}
}

func TestEgressClientAccess(t *testing.T) {
	if got := EgressClient(context.Background()); got != nil {
		t.Fatalf("unbound egress client = %p, want nil", got)
	}
	client := &http.Client{}
	if got := EgressClient(withEgressClient(context.Background(), client)); got != client {
		t.Fatalf("bound egress client = %p, want %p", got, client)
	}
}

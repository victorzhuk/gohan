package stores_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	stdcontext "github.com/victorzhuk/gohan/std/context"
)

func TestProjectionSatisfiesTruncate(t *testing.T) {
	var _ stores.Projection = stdcontext.Truncate{}
	var _ stores.Projection = stdcontext.NewTruncate()
}

func TestContextPolicyFields(t *testing.T) {
	p := stores.ContextPolicy{
		Projections: []stores.Projection{stubProjection{}},
	}
	if len(p.Projections) != 1 {
		t.Fatalf("Projections = %v, want one entry", p.Projections)
	}
	if p.Compactor != nil {
		t.Fatalf("Compactor = %v, want nil", p.Compactor)
	}
	fields := reflect.TypeOf(p)
	want := []reflect.StructField{
		{Name: "Projections", Type: reflect.TypeOf([]stores.Projection{})},
		{Name: "Compactor", Type: reflect.TypeOf((*stores.Compactor)(nil)).Elem()},
	}
	if fields.NumField() != len(want) {
		t.Fatalf("ContextPolicy has %d fields, want %d", fields.NumField(), len(want))
	}
	for i, w := range want {
		f := fields.Field(i)
		if f.Name != w.Name || f.Type != w.Type {
			t.Fatalf("field %d = %s %s, want %s %s", i, f.Name, f.Type, w.Name, w.Type)
		}
	}
}

type stubProjection struct{}

func (stubProjection) Project(_ context.Context, h stores.History, _ types.ModelProfile) (stores.History, error) {
	return h, nil
}

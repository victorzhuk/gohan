package stores

import (
	"errors"
	"fmt"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

func TestSchemaRegistry(t *testing.T) {
	t.Run("upcasters apply N to N+1 in order", func(t *testing.T) {
		r := NewSchemaRegistryFor(3)
		if err := r.Register("Message", 1, func(raw []byte) ([]byte, error) {
			return append(raw, 'b'), nil
		}); err != nil {
			t.Fatalf("Register from version 1: %v", err)
		}
		if err := r.Register("Message", 2, func(raw []byte) ([]byte, error) {
			return append(raw, 'c'), nil
		}); err != nil {
			t.Fatalf("Register from version 2: %v", err)
		}
		got, err := r.Upcast("Message", 1, []byte("a"))
		if err != nil {
			t.Fatalf("Upcast from version 1: %v", err)
		}
		if string(got) != "abc" {
			t.Fatalf("Upcast chain: got %q, want %q", got, "abc")
		}
	})

	t.Run("record at current version is unchanged", func(t *testing.T) {
		r := NewSchemaRegistryFor(2)
		if err := r.Register("Message", 1, func(raw []byte) ([]byte, error) {
			t.Fatal("upcaster ran for a current record")
			return raw, nil
		}); err != nil {
			t.Fatalf("Register from version 1: %v", err)
		}
		got, err := r.Upcast("Message", 2, []byte("a"))
		if err != nil {
			t.Fatalf("Upcast at current version: %v", err)
		}
		if string(got) != "a" {
			t.Fatalf("Upcast at current version: got %q, want %q", got, "a")
		}
	})

	t.Run("missing upcaster is ErrSchemaTooOld", func(t *testing.T) {
		r := NewSchemaRegistryFor(2)
		if _, err := r.Upcast("Message", 1, []byte("a")); !errors.Is(err, types.ErrSchemaTooOld) {
			t.Fatalf("Upcast without chain: got %v, want ErrSchemaTooOld", err)
		}
	})

	t.Run("record newer than the registry is incompatible", func(t *testing.T) {
		r := NewSchemaRegistryFor(1)
		if _, err := r.Upcast("Message", 2, []byte("a")); !errors.Is(err, types.ErrCheckpointIncompatible) {
			t.Fatalf("Upcast from a newer version: got %v, want ErrCheckpointIncompatible", err)
		}
	})

	t.Run("duplicate upcaster is refused", func(t *testing.T) {
		r := NewSchemaRegistryFor(2)
		up := func(raw []byte) ([]byte, error) { return raw, nil }
		if err := r.Register("Message", 1, up); err != nil {
			t.Fatalf("Register from version 1: %v", err)
		}
		if err := r.Register("Message", 1, up); err == nil {
			t.Fatal("Register duplicate upcaster: want error, got nil")
		}
	})

	t.Run("declared shapes stay registered", func(t *testing.T) {
		r := NewSchemaRegistryFor(2)
		if err := r.Register("Message", 1, func(raw []byte) ([]byte, error) {
			return append(raw, 'b'), nil
		}); err != nil {
			t.Fatalf("Register from version 1: %v", err)
		}
		if err := r.Declare("Message", 1); err != nil {
			t.Fatalf("Declare at version 1: %v", err)
		}
		if got := r.Unregistered(); len(got) != 0 {
			t.Fatalf("Unregistered after full chain: got %v, want none", got)
		}
		if err := r.Declare("State", 1); err != nil {
			t.Fatalf("Declare at version 1: %v", err)
		}
		got := r.Unregistered()
		if len(got) != 1 || got[0] != "State" {
			t.Fatalf("Unregistered with a hole: got %v, want [State]", got)
		}
	})

	r := NewSchemaRegistry()
	missing := r.Unregistered()
	fmt.Printf("unregistered=%d\n", len(missing))
	if len(missing) != 0 {
		t.Fatalf("Unregistered: got %v, want none", missing)
	}
}

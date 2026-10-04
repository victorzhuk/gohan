package storetest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

// SchemaVersion is the version of a stored record's serialized shape. Zero
// means the record predates the field and is version 1.
type SchemaVersion int

// schemaCurrent is the shape version the current release writes. It mirrors
// the registry constant so the suite never imports a concrete store.
const schemaCurrent SchemaVersion = 1

// Upcaster converts one record from version N to N+1.
type Upcaster func(raw []byte) ([]byte, error)

// SchemaRegistry is the port under test. Implementations adapt to it in
// their own binding tests.
type SchemaRegistry interface {
	Declare(shape string, v SchemaVersion) error
	Register(shape string, from SchemaVersion, up Upcaster) error
	Upcast(shape string, from SchemaVersion, raw []byte) ([]byte, error)
	Unregistered() []string
}

// SchemasFactory builds a registry whose readers target the given current
// version. Implementations map versions below their release's current onto
// the same upcaster chain readers use.
type SchemasFactory func(ctx context.Context, current SchemaVersion) (SchemaRegistry, error)

// SchemaFixture is a record written by a released schema version.
type SchemaFixture struct {
	Shape   string
	Version SchemaVersion
	Data    string
}

// schemaFixtures are recorded shapes replayed through current readers. Each
// released version of every stored shape gets one entry here; the entry stays
// once added so older records keep a covered upcast path forever.
var schemaFixtures = []SchemaFixture{
	{"Message", 1, `{"role":"user","blocks":[{"type":"text","text":"hi"}]}`},
	{"State", 1, `{"values":{"topic":"gohan"},"version":7}`},
	{"Checkpoint", 1, `{"token":"tok","expires_at":"2026-01-01T00:00:00Z"}`},
	{"AuditRecord", 1, `{"kind":"tool","hash":"abc123"}`},
	{"Event", 1, `{"name":"gohan.session.forked","tenant":"t1"}`},
	{"Entry", 1, `{"key":"k1","outcome":"succeeded"}`},
	{"Run", 1, `{"state":"running","attempt":1}`},
	{"RedactionMap", 1, `{"paths":["a.b"]}`},
}

// Schemas runs the schema registry conformance suite against one
// implementation. The purity of an upcaster cannot be proven from outside,
// so the suite asserts determinism instead: two upcasts of the same input
// are byte-identical.
func Schemas(t *testing.T, factory SchemasFactory) {
	t.Helper()
	t.Run("fixtures_replay_at_current", func(t *testing.T) {
		r := openRegistry(t, factory, schemaCurrent)
		for _, f := range schemaFixtures {
			f := f
			t.Run(f.Shape, func(t *testing.T) {
				if f.Version > schemaCurrent {
					t.Fatalf("fixture %s at version %d is newer than current %d", f.Shape, f.Version, schemaCurrent)
				}
				if f.Version < schemaCurrent {
					if err := r.Declare(f.Shape, f.Version); err != nil {
						t.Fatalf("Declare %s at version %d: %v", f.Shape, f.Version, err)
					}
				}
				first, err := r.Upcast(f.Shape, f.Version, []byte(f.Data))
				if err != nil {
					t.Fatalf("Upcast %s from version %d has no reachable path to current: %v", f.Shape, f.Version, err)
				}
				if !json.Valid(first) {
					t.Fatalf("Upcast %s produced invalid JSON: %q", f.Shape, first)
				}
				second, err := r.Upcast(f.Shape, f.Version, []byte(f.Data))
				if err != nil {
					t.Fatalf("Upcast %s again: %v", f.Shape, err)
				}
				if !bytes.Equal(first, second) {
					t.Fatalf("Upcast %s is not deterministic: %q vs %q", f.Shape, first, second)
				}
				if f.Version == schemaCurrent && !bytes.Equal(first, []byte(f.Data)) {
					t.Fatalf("record already at current version was rewritten: %q vs %q", first, f.Data)
				}
			})
		}
	})

	t.Run("no_unregistered_shapes_at_current", func(t *testing.T) {
		r := openRegistry(t, factory, schemaCurrent)
		for _, f := range schemaFixtures {
			if f.Version < schemaCurrent {
				if err := r.Declare(f.Shape, f.Version); err != nil {
					t.Fatalf("Declare %s at version %d: %v", f.Shape, f.Version, err)
				}
			}
		}
		if missing := r.Unregistered(); len(missing) != 0 {
			t.Fatalf("Unregistered at current version: got %v, want none", missing)
		}
	})

	t.Run("upcast_chain_applies_in_order", func(t *testing.T) {
		const target = schemaCurrent + 2
		r := openRegistry(t, factory, target)
		const shape = "SuiteChainShape"
		if err := r.Declare(shape, 1); err != nil {
			t.Fatalf("Declare at version 1: %v", err)
		}
		if err := r.Register(shape, 1, func(raw []byte) ([]byte, error) {
			return append(raw, 'b'), nil
		}); err != nil {
			t.Fatalf("Register from version 1: %v", err)
		}
		if err := r.Register(shape, 2, func(raw []byte) ([]byte, error) {
			return append(raw, 'c'), nil
		}); err != nil {
			t.Fatalf("Register from version 2: %v", err)
		}
		got, err := r.Upcast(shape, 1, []byte("a"))
		if err != nil {
			t.Fatalf("Upcast chain from version 1: %v", err)
		}
		if string(got) != "abc" {
			t.Fatalf("Upcast chain: got %q, want %q", got, "abc")
		}
	})

	t.Run("upcast_is_deterministic", func(t *testing.T) {
		const target = schemaCurrent + 2
		r := openRegistry(t, factory, target)
		const shape = "SuiteDeterministicShape"
		if err := r.Declare(shape, 1); err != nil {
			t.Fatalf("Declare at version 1: %v", err)
		}
		if err := r.Register(shape, 1, func(raw []byte) ([]byte, error) {
			return append(raw, '-'), nil
		}); err != nil {
			t.Fatalf("Register from version 1: %v", err)
		}
		if err := r.Register(shape, 2, func(raw []byte) ([]byte, error) {
			return append(raw, '='), nil
		}); err != nil {
			t.Fatalf("Register from version 2: %v", err)
		}
		for _, from := range []SchemaVersion{1, 2} {
			first, err := r.Upcast(shape, from, []byte("x"))
			if err != nil {
				t.Fatalf("Upcast from version %d: %v", from, err)
			}
			second, err := r.Upcast(shape, from, []byte("x"))
			if err != nil {
				t.Fatalf("Upcast from version %d again: %v", from, err)
			}
			if !bytes.Equal(first, second) {
				t.Fatalf("Upcast from version %d is not deterministic: %q vs %q", from, first, second)
			}
		}
	})

	t.Run("missing_upcast_path_listed", func(t *testing.T) {
		const target = schemaCurrent + 2
		r := openRegistry(t, factory, target)
		const shape = "SuiteMissingShape"
		if err := r.Declare(shape, 1); err != nil {
			t.Fatalf("Declare at version 1: %v", err)
		}
		if _, err := r.Upcast(shape, 1, []byte("{}")); !errors.Is(err, types.ErrSchemaTooOld) {
			t.Fatalf("Upcast without a chain: got %v, want ErrSchemaTooOld", err)
		}
		if missing := r.Unregistered(); !contains(missing, shape) {
			t.Fatalf("Unregistered: got %v, want it to list %s", missing, shape)
		}
	})

	t.Run("newer_record_is_incompatible", func(t *testing.T) {
		r := openRegistry(t, factory, schemaCurrent)
		const shape = "SuiteNewerShape"
		if _, err := r.Upcast(shape, schemaCurrent+1, []byte("{}")); !errors.Is(err, types.ErrCheckpointIncompatible) {
			t.Fatalf("Upcast of a newer record: got %v, want ErrCheckpointIncompatible", err)
		}
	})

	t.Run("declare_outside_range_refused", func(t *testing.T) {
		r := openRegistry(t, factory, schemaCurrent)
		const shape = "SuiteRangeShape"
		for _, v := range []SchemaVersion{0, schemaCurrent + 1} {
			if err := r.Declare(shape, v); err == nil {
				t.Fatalf("Declare at version %d outside 1..%d was accepted", v, schemaCurrent)
			}
		}
	})
}

func openRegistry(t *testing.T, factory SchemasFactory, current SchemaVersion) SchemaRegistry {
	t.Helper()
	r, err := factory(context.Background(), current)
	if err != nil {
		t.Fatalf("open registry at current version %d: %v", current, err)
	}
	if r == nil {
		t.Fatalf("open registry at current version %d: nil registry", current)
	}
	return r
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

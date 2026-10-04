package stores

import (
	"fmt"
	"sync"

	"github.com/victorzhuk/gohan/core/types"
)

// SchemaVersion is the version of a stored record's serialized shape. Zero
// means the record predates the field and is version 1.
type SchemaVersion int

// CurrentSchemaVersion is the shape version this release writes.
const CurrentSchemaVersion SchemaVersion = 1

// Upcaster converts one record from version N to N+1. It operates on the
// serialized record and must be a pure function of its input. The signature
// carries no clock, lookup or randomness, so an upcaster cannot depend on
// them.
type Upcaster func(raw []byte) ([]byte, error)

// storedShapes are the shapes persisted by this release. Every one of them
// carries a SchemaVersion and is declared in each registry.
var storedShapes = []string{
	"Message",
	"State",
	"Checkpoint",
	"AuditRecord",
	"Event",
	"Entry",
	"Run",
	"RedactionMap",
}

// SchemaRegistry maps a stored shape to the chain of pure upcasters applied
// on read. Upcasters are N->N+1 and are applied in order; additive changes
// need none. Stores never rewrite records in place.
type SchemaRegistry struct {
	mu       sync.Mutex
	current  SchemaVersion
	declared map[string]SchemaVersion
	up       map[string]map[SchemaVersion]Upcaster
}

// NewSchemaRegistry returns a registry for the shapes this release writes,
// all at CurrentSchemaVersion.
func NewSchemaRegistry() *SchemaRegistry {
	return NewSchemaRegistryFor(CurrentSchemaVersion)
}

// NewSchemaRegistryFor returns a registry whose target version is current.
// Readers use it to run fixtures written under older versions through the
// same upcaster chain.
func NewSchemaRegistryFor(current SchemaVersion) *SchemaRegistry {
	r := &SchemaRegistry{
		current:  current,
		declared: make(map[string]SchemaVersion, len(storedShapes)),
		up:       make(map[string]map[SchemaVersion]Upcaster),
	}
	for _, shape := range storedShapes {
		r.declared[shape] = current
	}
	return r
}

// Declare records that shape is stored at version v. Readers replaying old
// fixtures declare the version those fixtures were written at.
func (r *SchemaRegistry) Declare(shape string, v SchemaVersion) error {
	if v < 1 || v > r.current {
		return fmt.Errorf("gohan: declare %s at version %d outside 1..%d", shape, v, r.current)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.declared[shape] = v
	return nil
}

// Register adds the upcaster from version from to from+1 for shape.
func (r *SchemaRegistry) Register(shape string, from SchemaVersion, up Upcaster) error {
	if from < 1 || from >= r.current {
		return fmt.Errorf("gohan: upcaster for %s must start at 1..%d", shape, r.current-1)
	}
	if up == nil {
		return fmt.Errorf("gohan: upcaster for %s at version %d is nil", shape, from)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	chain := r.up[shape]
	if chain == nil {
		chain = make(map[SchemaVersion]Upcaster)
		r.up[shape] = chain
	}
	if _, dup := chain[from]; dup {
		return fmt.Errorf("gohan: upcaster for %s at version %d already registered", shape, from)
	}
	chain[from] = up
	return nil
}

// Upcast applies the registered chain to bring raw from version from up to
// the registry's current version. A record already current is returned
// unchanged.
func (r *SchemaRegistry) Upcast(shape string, from SchemaVersion, raw []byte) ([]byte, error) {
	if from == 0 {
		from = 1
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if from > r.current {
		return nil, fmt.Errorf("gohan: %s at version %d is newer than %d: %w", shape, from, r.current, types.ErrCheckpointIncompatible)
	}
	for v := from; v < r.current; v++ {
		up, ok := r.up[shape][v]
		if !ok {
			return nil, fmt.Errorf("gohan: %s has no upcaster from version %d: %w", shape, v, types.ErrSchemaTooOld)
		}
		next, err := up(raw)
		if err != nil {
			return nil, fmt.Errorf("gohan: upcast %s from version %d: %w", shape, v, err)
		}
		raw = next
	}
	return raw, nil
}

// Unregistered lists declared shapes that are not at the current version and
// have no complete upcaster chain to it. An empty list means every stored
// shape is readable at the current version.
func (r *SchemaRegistry) Unregistered() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var missing []string
	for shape, v := range r.declared {
		ok := true
		for step := v; step < r.current; step++ {
			if _, found := r.up[shape][step]; !found {
				ok = false
				break
			}
		}
		if !ok {
			missing = append(missing, shape)
		}
	}
	return missing
}

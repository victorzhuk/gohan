package storetest

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

// fakeRegistry mirrors the registry contract so the suite can run without a
// concrete store.
type fakeRegistry struct {
	mu       sync.Mutex
	current  SchemaVersion
	declared map[string]SchemaVersion
	up       map[string]map[SchemaVersion]Upcaster
}

func newFakeRegistry(current SchemaVersion) *fakeRegistry {
	return &fakeRegistry{
		current:  current,
		declared: map[string]SchemaVersion{},
		up:       map[string]map[SchemaVersion]Upcaster{},
	}
}

func (r *fakeRegistry) Declare(shape string, v SchemaVersion) error {
	if v < 1 || v > r.current {
		return fmt.Errorf("fake: version %d outside 1..%d", v, r.current)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.declared[shape] = v
	return nil
}

func (r *fakeRegistry) Register(shape string, from SchemaVersion, up Upcaster) error {
	if from < 1 || from >= r.current {
		return fmt.Errorf("fake: from %d outside 1..%d", from, r.current-1)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	chain := r.up[shape]
	if chain == nil {
		chain = map[SchemaVersion]Upcaster{}
		r.up[shape] = chain
	}
	if _, dup := chain[from]; dup {
		return fmt.Errorf("fake: duplicate upcaster at %d", from)
	}
	chain[from] = up
	return nil
}

func (r *fakeRegistry) Upcast(shape string, from SchemaVersion, raw []byte) ([]byte, error) {
	if from == 0 {
		from = 1
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if from > r.current {
		return nil, fmt.Errorf("fake: version %d newer than %d: %w", from, r.current, types.ErrCheckpointIncompatible)
	}
	for v := from; v < r.current; v++ {
		up, ok := r.up[shape][v]
		if !ok {
			return nil, fmt.Errorf("fake: %s has no upcaster from %d: %w", shape, v, types.ErrSchemaTooOld)
		}
		next, err := up(raw)
		if err != nil {
			return nil, err
		}
		raw = next
	}
	return raw, nil
}

func (r *fakeRegistry) Unregistered() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var missing []string
	for shape, v := range r.declared {
		if v == r.current {
			continue
		}
		complete := true
		for u := v; u < r.current; u++ {
			if _, ok := r.up[shape][u]; !ok {
				complete = false
				break
			}
		}
		if !complete {
			missing = append(missing, shape)
		}
	}
	return missing
}

func TestStoretestSchemasUnit(t *testing.T) {
	Schemas(t, func(_ context.Context, current SchemaVersion) (SchemaRegistry, error) {
		r := newFakeRegistry(current)
		for _, f := range schemaFixtures {
			r.declared[f.Shape] = f.Version
			for v := f.Version; v < current; v++ {
				if r.up[f.Shape] == nil {
					r.up[f.Shape] = map[SchemaVersion]Upcaster{}
				}
				r.up[f.Shape][v] = func(raw []byte) ([]byte, error) {
					return append(bytes.Clone(raw), ' '), nil
				}
			}
		}
		return r, nil
	})
}

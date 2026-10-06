package gohan

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/testkit/storetest"
)

// schemaRegistryAdapter converts between the store's own named types and the
// suite's aliases so the suite never imports core/stores.
type schemaRegistryAdapter struct {
	inner *stores.SchemaRegistry
}

func (a schemaRegistryAdapter) Declare(shape string, v storetest.SchemaVersion) error {
	return a.inner.Declare(shape, stores.SchemaVersion(v))
}

func (a schemaRegistryAdapter) Register(shape string, from storetest.SchemaVersion, up storetest.Upcaster) error {
	return a.inner.Register(shape, stores.SchemaVersion(from), stores.Upcaster(up))
}

func (a schemaRegistryAdapter) Upcast(shape string, from storetest.SchemaVersion, raw []byte) ([]byte, error) {
	return a.inner.Upcast(shape, stores.SchemaVersion(from), raw)
}

func (a schemaRegistryAdapter) Unregistered() []string {
	return a.inner.Unregistered()
}

func TestStoretestSchemasBind(t *testing.T) {
	storetest.Schemas(t, func(_ context.Context, current storetest.SchemaVersion) (storetest.SchemaRegistry, error) {
		registry := stores.NewSchemaRegistryFor(stores.SchemaVersion(current))
		for _, shape := range []string{"Message", "Checkpoint"} {
			for from := storetest.SchemaVersion(1); from < current; from++ {
				from := from
				version := from + 1
				if err := registry.Register(shape, stores.SchemaVersion(from), func(raw []byte) ([]byte, error) {
					var record map[string]json.RawMessage
					if err := json.Unmarshal(raw, &record); err != nil {
						return nil, err
					}
					record[fmt.Sprintf("schema_v%d", version)] = json.RawMessage("true")
					return json.Marshal(record)
				}); err != nil {
					return nil, err
				}
			}
		}
		return schemaRegistryAdapter{inner: registry}, nil
	})
}

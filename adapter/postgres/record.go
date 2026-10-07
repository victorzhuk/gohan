package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/victorzhuk/gohan/core/stores"
)

func encodeRecord(shape string, payload any) ([]byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", shape, err)
	}
	envelope, err := json.Marshal(stores.RecordEnvelope{Shape: shape, SchemaVersion: int(stores.CurrentSchemaVersion), Payload: raw})
	if err != nil {
		return nil, fmt.Errorf("encode %s envelope: %w", shape, err)
	}
	return envelope, nil
}

func envelope(raw []byte) (stores.RecordEnvelope, error) {
	var env stores.RecordEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return stores.RecordEnvelope{}, fmt.Errorf("decode envelope: %w", err)
	}
	return env, nil
}

func decode[T any](ctx context.Context, reg *stores.SchemaRegistry, raw []byte) (T, error) {
	env, err := envelope(raw)
	if err != nil {
		var zero T
		return zero, err
	}
	return stores.DecodeRecord[T](ctx, reg, env)
}

package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// Outputs is the content-addressed blob store on Postgres: equal bytes
// yield one ref, and every Put claims the ref for the session's owner.
type Outputs struct {
	db *DB
}

// NewOutputs builds the output store on db.
func NewOutputs(db *DB) *Outputs {
	return &Outputs{db: db}
}

func encodeOutput(content []types.Block) ([]byte, error) {
	return json.Marshal(types.Message{Blocks: content})
}

// Put encodes content canonically, stores it once under its content hash
// and claims the ref for the run's owner.
func (s *Outputs) Put(ctx context.Context, ri types.RunInfo, content []types.Block) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("output put: %w", err)
	}
	encoded, err := encodeOutput(content)
	if err != nil {
		return "", fmt.Errorf("output encode: %w", err)
	}
	sum := sha256.Sum256(encoded)
	ref := hex.EncodeToString(sum[:])
	if _, err := s.db.pool.Exec(ctx,
		`INSERT INTO output_record (ref, tenant, subject, content, payload) VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (ref) DO NOTHING`,
		ref, ri.Principal.Tenant, ri.Principal.Subject, encoded, encoded); err != nil {
		return "", fmt.Errorf("output put: %w", err)
	}
	return ref, nil
}

// Get returns a copy of the stored bytes for ref.
func (s *Outputs) Get(ctx context.Context, ref string) ([]byte, error) {
	var raw []byte
	err := s.db.pool.QueryRow(ctx,
		`SELECT payload FROM output_record WHERE ref = $1`, ref).Scan(&raw)
	if err != nil {
		return nil, fmt.Errorf("output %s: %w", ref, stores.ErrOutputNotFound)
	}
	return slices.Clone(raw), nil
}

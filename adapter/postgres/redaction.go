package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// RedactionMaps is the tenant-keyed redaction-map store on Postgres.
type RedactionMaps struct {
	db      *DB
	schemas *stores.SchemaRegistry
	region  string
}

// RedactionMapsOption configures NewRedactionMaps.
type RedactionMapsOption func(*RedactionMaps)

// WithRedactionRegion names the deployment region the maps live in.
func WithRedactionRegion(region string) RedactionMapsOption {
	return func(s *RedactionMaps) { s.region = region }
}

// NewRedactionMaps builds the redaction-map store on db.
func NewRedactionMaps(db *DB, opts ...RedactionMapsOption) *RedactionMaps {
	s := &RedactionMaps{db: db, schemas: stores.NewSchemaRegistry()}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Region names the deployment region the maps live in.
func (s *RedactionMaps) Region() string { return s.region }

// Put stores the map for the tenant's session.
func (s *RedactionMaps) Put(ctx context.Context, tenant, session string, m types.RedactionMap) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if tenant == "" || session == "" {
		return errors.New("tenant and session are required")
	}
	if (m.Tenant != "" && m.Tenant != tenant) || (m.Session != "" && m.Session != session) {
		return errors.New("redaction map identity mismatch")
	}
	m.Tenant, m.Session = tenant, session
	raw, err := encodeRecord("RedactionMap", m)
	if err != nil {
		return err
	}
	if _, err := s.db.pool.Exec(ctx,
		`INSERT INTO redaction_map (tenant, session, payload) VALUES ($1, $2, $3)
		 ON CONFLICT (tenant, session) DO UPDATE SET payload = $3`,
		tenant, session, raw); err != nil {
		return fmt.Errorf("put redaction map: %w", err)
	}
	return nil
}

// Get returns the tenant's map for the session, or
// stores.ErrRedactionMapNotFound.
func (s *RedactionMaps) Get(ctx context.Context, tenant, session string) (types.RedactionMap, error) {
	if err := ctx.Err(); err != nil {
		return types.RedactionMap{}, err
	}
	var raw []byte
	err := s.db.pool.QueryRow(ctx,
		`SELECT payload FROM redaction_map WHERE tenant = $1 AND session = $2`,
		tenant, session).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return types.RedactionMap{}, stores.ErrRedactionMapNotFound
	}
	if err != nil {
		return types.RedactionMap{}, fmt.Errorf("get redaction map: %w", err)
	}
	m, err := decode[types.RedactionMap](ctx, s.schemas, raw)
	if err != nil {
		return types.RedactionMap{}, err
	}
	m.Entries = cloneRedactionEntries(m.Entries)
	return m, nil
}

func cloneRedactionEntries(entries map[string]types.Entity) map[string]types.Entity {
	clone := make(map[string]types.Entity, len(entries))
	for token, entity := range entries {
		clone[token] = entity
	}
	return clone
}

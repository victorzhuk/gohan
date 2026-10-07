package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// Notes is the versioned notes primitive on Postgres. Read is
// unconditional; Write is a compare-and-swap on the stored version.
type Notes struct {
	db *DB
}

// NewNotes builds the notes store on db.
func NewNotes(db *DB) *Notes {
	return &Notes{db: db}
}

// Read returns the notes and their version for the key; a missing record
// reads as empty at version 0.
func (s *Notes) Read(ctx context.Context, key stores.NotesKey) (string, int64, error) {
	var notes string
	var version int64
	err := s.db.pool.QueryRow(ctx,
		`SELECT notes, version FROM notes_record WHERE scope = $1 AND tenant = $2 AND subject = $3 AND session = $4`,
		int(key.Scope), key.Tenant, key.Subject, key.Session).Scan(&notes, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, nil
	}
	if err != nil {
		return "", 0, fmt.Errorf("read notes %v: %w", key, err)
	}
	return notes, version, nil
}

// Write swaps the notes when expectedVersion still matches the stored one;
// the loser of a race fails with types.ErrVersionConflict.
func (s *Notes) Write(ctx context.Context, key stores.NotesKey, expectedVersion int64, notes string) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("write notes %v: %w", key, err)
	}
	if expectedVersion == 0 {
		return s.insert(ctx, key, notes)
	}
	return s.update(ctx, key, expectedVersion, notes)
}

func (s *Notes) insert(ctx context.Context, key stores.NotesKey, notes string) (int64, error) {
	var version int64
	err := s.db.pool.QueryRow(ctx,
		`INSERT INTO notes_record (scope, tenant, subject, session, notes, version)
		 VALUES ($1, $2, $3, $4, $5, 1)
		 ON CONFLICT (scope, tenant, subject, session)
		 DO UPDATE SET notes = $5, version = 1
		 WHERE notes_record.version = 0
		 RETURNING version`,
		int(key.Scope), key.Tenant, key.Subject, key.Session, notes).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, conflict(key, 0)
	}
	if err != nil {
		return 0, fmt.Errorf("write notes %v: %w", key, err)
	}
	return version, nil
}

func (s *Notes) update(ctx context.Context, key stores.NotesKey, expected int64, notes string) (int64, error) {
	tag, err := s.db.pool.Exec(ctx,
		`UPDATE notes_record SET notes = $5, version = $6
		 WHERE scope = $1 AND tenant = $2 AND subject = $3 AND session = $4 AND version = $6 - 1`,
		int(key.Scope), key.Tenant, key.Subject, key.Session, notes, expected+1)
	if err != nil {
		return 0, fmt.Errorf("write notes %v: %w", key, err)
	}
	if tag.RowsAffected() == 0 {
		return 0, conflict(key, expected)
	}
	return expected + 1, nil
}

func conflict(key stores.NotesKey, expected int64) error {
	return fmt.Errorf("write notes %v at version %d: %w", key, expected, types.ErrVersionConflict)
}

package postgres

import (
	"context"
	"errors"

	"github.com/victorzhuk/gohan/core/types"
)

// Build wires the full postgres store set for the environment the run
// cost tags name. WithAutoMigrate is development-only: a stack built for
// a production environment is refused before any connection is opened.
func Build(ctx context.Context, dsn string, tags types.CostTags, opts ...Option) (*Stores, error) {
	var c dbConfig
	c.schema = DefaultSchema
	for _, o := range opts {
		o(&c)
	}
	if c.autoMigrate && tags.Environment == "production" {
		return nil, errors.New("postgres.Build: WithAutoMigrate is refused when CostTags.Environment is production")
	}
	db, err := New(ctx, dsn, opts...)
	if err != nil {
		return nil, err
	}
	return &Stores{
		Sessions:    NewSessionLog(db),
		Runs:        NewRuns(db),
		Events:      NewEventLog(db),
		Checkpoints: NewCheckpoints(db),
		Journal:     NewJournal(db),
		Outputs:     NewOutputs(db),
		Notes:       NewNotes(db),
		Redaction:   NewRedactionMaps(db),
		Audit:       NewAuditLog(db),
	}, nil
}

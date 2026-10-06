package stores

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/victorzhuk/gohan/core/types"
)

var ErrRedactionMapNotFound = errors.New("redaction map not found")

type RedactionMaps interface {
	Put(ctx context.Context, tenant, session string, m types.RedactionMap) error
	Get(ctx context.Context, tenant, session string) (types.RedactionMap, error)
}

type MemoryRedactionMaps struct {
	mu      sync.RWMutex
	maps    map[redactionMapKey][]byte
	region  string
	schemas *SchemaRegistry
}

type MemoryRedactionMapsOption func(*MemoryRedactionMaps)

// WithMemoryRedactionMapsSchemas injects the schema registry record reads
// decode through.
func WithMemoryRedactionMapsSchemas(reg *SchemaRegistry) MemoryRedactionMapsOption {
	return func(s *MemoryRedactionMaps) { s.schemas = reg }
}

func WithMemoryRedactionMapsRegion(region string) MemoryRedactionMapsOption {
	return func(s *MemoryRedactionMaps) { s.region = region }
}

func (s *MemoryRedactionMaps) Region() string { return s.region }

type redactionMapKey struct {
	tenant  string
	session string
}

func NewMemoryRedactionMaps(opts ...MemoryRedactionMapsOption) *MemoryRedactionMaps {
	s := &MemoryRedactionMaps{maps: make(map[redactionMapKey][]byte), schemas: NewSchemaRegistry()}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *MemoryRedactionMaps) Put(ctx context.Context, tenant, session string, m types.RedactionMap) error {
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
	m.Entries = cloneRedactionEntries(m.Entries)
	raw, err := encodeEnvelope("RedactionMap", m)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.maps == nil {
		s.maps = make(map[redactionMapKey][]byte)
	}
	s.maps[redactionMapKey{tenant: tenant, session: session}] = raw
	return nil
}

func (s *MemoryRedactionMaps) Get(ctx context.Context, tenant, session string) (types.RedactionMap, error) {
	if err := ctx.Err(); err != nil {
		return types.RedactionMap{}, err
	}
	s.mu.RLock()
	raw, ok := s.maps[redactionMapKey{tenant: tenant, session: session}]
	s.mu.RUnlock()
	if !ok {
		return types.RedactionMap{}, ErrRedactionMapNotFound
	}
	var env RecordEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return types.RedactionMap{}, fmt.Errorf("decode RedactionMap envelope: %w", err)
	}
	m, err := DecodeRecord[types.RedactionMap](ctx, s.schemas, env)
	if err != nil {
		return types.RedactionMap{}, err
	}
	m.Entries = cloneRedactionEntries(m.Entries)
	return m, nil
}

// SeedRecord installs envelope as the stored bytes of the tenant's map for
// the session, so the next public read consumes them as persisted bytes.
// Seeding bypasses identity checks: fixtures replay historical bytes, not
// client writes.
func (s *MemoryRedactionMaps) SeedRecord(_ context.Context, identity string, envelope RecordEnvelope) error {
	raw, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("seed %s: %w", identity, err)
	}
	key, err := redactionMapIdentity(identity)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.maps[key]; !ok {
		return fmt.Errorf("record identity %s: not found", identity)
	}
	s.maps[key] = raw
	return nil
}

// StoredRecord returns the stored bytes of identity unchanged.
func (s *MemoryRedactionMaps) StoredRecord(_ context.Context, identity string) ([]byte, error) {
	key, err := redactionMapIdentity(identity)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	raw, ok := s.maps[key]
	if !ok {
		return nil, fmt.Errorf("record identity %s: not found", identity)
	}
	return append([]byte(nil), raw...), nil
}

// redactionMapIdentity resolves a "redaction/<tenant>/<session>" identity.
// Tenants and session ids must not carry a slash.
func redactionMapIdentity(identity string) (redactionMapKey, error) {
	rest, ok := strings.CutPrefix(identity, "redaction/")
	if !ok {
		return redactionMapKey{}, fmt.Errorf("record identity %s: not a redaction map identity", identity)
	}
	tenant, session, ok := strings.Cut(rest, "/")
	if !ok || tenant == "" || session == "" || strings.Contains(session, "/") {
		return redactionMapKey{}, fmt.Errorf("record identity %s: bad identity", identity)
	}
	return redactionMapKey{tenant: tenant, session: session}, nil
}

func cloneRedactionEntries(entries map[string]types.Entity) map[string]types.Entity {
	clone := make(map[string]types.Entity, len(entries))
	for token, entity := range entries {
		clone[token] = entity
	}
	return clone
}

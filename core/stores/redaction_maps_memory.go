package stores

import (
	"context"
	"errors"
	"sync"

	"github.com/victorzhuk/gohan/core/types"
)

var ErrRedactionMapNotFound = errors.New("redaction map not found")

type RedactionMaps interface {
	Put(ctx context.Context, tenant, session string, m types.RedactionMap) error
	Get(ctx context.Context, tenant, session string) (types.RedactionMap, error)
}

type MemoryRedactionMaps struct {
	mu     sync.RWMutex
	maps   map[redactionMapKey]types.RedactionMap
	region string
}

type MemoryRedactionMapsOption func(*MemoryRedactionMaps)

func WithMemoryRedactionMapsRegion(region string) MemoryRedactionMapsOption {
	return func(s *MemoryRedactionMaps) { s.region = region }
}

func (s *MemoryRedactionMaps) Region() string { return s.region }

type redactionMapKey struct {
	tenant  string
	session string
}

func NewMemoryRedactionMaps(opts ...MemoryRedactionMapsOption) *MemoryRedactionMaps {
	s := &MemoryRedactionMaps{maps: make(map[redactionMapKey]types.RedactionMap)}
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
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.maps == nil {
		s.maps = make(map[redactionMapKey]types.RedactionMap)
	}
	s.maps[redactionMapKey{tenant: tenant, session: session}] = m
	return nil
}

func (s *MemoryRedactionMaps) Get(ctx context.Context, tenant, session string) (types.RedactionMap, error) {
	if err := ctx.Err(); err != nil {
		return types.RedactionMap{}, err
	}
	s.mu.RLock()
	m, ok := s.maps[redactionMapKey{tenant: tenant, session: session}]
	s.mu.RUnlock()
	if !ok {
		return types.RedactionMap{}, ErrRedactionMapNotFound
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

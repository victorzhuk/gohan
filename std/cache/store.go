package cache

import (
	"sync"

	"github.com/victorzhuk/gohan/core/types"
)

// Store is the exact-match memory store behind the seam. Scoping lives in
// the key: Key folds the tenant and the scope class in, so an entry written
// for one tenant or scope class never resolves for another.
type Store struct {
	mu      sync.RWMutex
	entries map[string]types.ModelRequest
}

// NewStore returns an empty in-memory store.
func NewStore() *Store {
	return &Store{entries: make(map[string]types.ModelRequest)}
}

// Get reports the prefix stored under key, or ok == false on a miss.
func (s *Store) Get(key string) (types.ModelRequest, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	req, ok := s.entries[key]
	return req, ok
}

// Put stores the prefix under key, replacing any previous entry.
func (s *Store) Put(key string, prefix types.ModelRequest) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[key] = prefix
}

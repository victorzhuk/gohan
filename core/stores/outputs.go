package stores

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/victorzhuk/gohan/core/types"
)

// InlineBlobBytes is the spill threshold: a block whose Data exceeds it is
// written to the OutputStore and persisted with Blob{Ref, SHA256, Bytes} and
// nil Data (messages, Blobs live once).
const InlineBlobBytes = 64 * 1024

// ErrOutputNotFound reports a ref the store never wrote or already erased.
var ErrOutputNotFound = errors.New("gohan: output ref not found")

// OutputStore holds content-addressed blobs: equal bytes yield one ref.
// Put stores blocks; Get returns the stored bytes, which is what blob
// assembly and read_output read (working-state, OutputStore).
type OutputStore interface {
	Put(ctx context.Context, ri types.RunInfo, content []types.Block) (ref string, err error)
	Get(ctx context.Context, ref string) ([]byte, error)
}

// encodeOutput renders content as the store's canonical bytes: the message
// block codec, which round-trips every block type deterministically.
func encodeOutput(content []types.Block) ([]byte, error) {
	encoded, err := json.Marshal(types.Message{Blocks: content})
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

type outputClaim struct {
	session string
	owner   types.SessionOwner
}

// MemoryOutputs is the in-memory OutputStore reference implementation.
// Refs carry the sha256 of the canonical block encoding, so a second Put of
// equal content lands on the same ref and the store keeps one copy. Every
// Put adds one claim per session; a ref lives until its last claim goes,
// through the session cascade, a fork or the subject erasure.
type MemoryOutputs struct {
	mu      sync.Mutex
	blobs   map[string][]byte
	claims  map[string]map[outputClaim]struct{}
	subject map[types.SessionOwner]map[string]struct{}
}

// NewMemoryOutputs builds an empty store.
func NewMemoryOutputs() *MemoryOutputs {
	return &MemoryOutputs{
		blobs:   make(map[string][]byte),
		claims:  make(map[string]map[outputClaim]struct{}),
		subject: make(map[types.SessionOwner]map[string]struct{}),
	}
}

// Put encodes content canonically, stores it once under its content hash and
// records a claim for the session and subject in ri, so the session and
// subject cascades can drop it. Content is copied: nothing the caller passed
// stays aliased.
func (s *MemoryOutputs) Put(_ context.Context, ri types.RunInfo, content []types.Block) (string, error) {
	encoded, err := encodeOutput(content)
	if err != nil {
		return "", fmt.Errorf("output encode: %w", err)
	}
	sum := sha256.Sum256(encoded)
	ref := hex.EncodeToString(sum[:])

	claim := outputClaim{session: ri.SessionID, owner: types.SessionOwner{Tenant: ri.Principal.Tenant, Subject: ri.Principal.Subject}}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.blobs[ref]; !ok {
		s.blobs[ref] = slices.Clone(encoded)
		s.claims[ref] = make(map[outputClaim]struct{})
	}
	s.claims[ref][claim] = struct{}{}
	if claim.owner.Subject != "" || claim.owner.Tenant != "" {
		if s.subject[claim.owner] == nil {
			s.subject[claim.owner] = make(map[string]struct{})
		}
		s.subject[claim.owner][ref] = struct{}{}
	}
	return ref, nil
}

// Get returns a copy of the stored bytes for ref.
func (s *MemoryOutputs) Get(_ context.Context, ref string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.blobs[ref]
	if !ok {
		return nil, fmt.Errorf("output %s: %w", ref, ErrOutputNotFound)
	}
	return slices.Clone(data), nil
}

// DeleteSessionDependents drops the session's claim on every ref it owns; a
// ref still claimed by another session stays. The session log's cascade
// treats an already gone session as done, so unknown ids are not an error.
func (s *MemoryOutputs) DeleteSessionDependents(_ context.Context, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ref, claims := range s.claims {
		for claim := range claims {
			if claim.session == sessionID {
				delete(claims, claim)
			}
		}
		s.dropIfUnclaimed(ref, claims)
	}
	return nil
}

// CopySessionDependents gives the fork a claim on every ref the parent owns,
// so deleting the parent keeps bytes the child still references.
func (s *MemoryOutputs) CopySessionDependents(_ context.Context, from, to string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, claims := range s.claims {
		for claim := range claims {
			if claim.session == from {
				claims[outputClaim{session: to, owner: claim.owner}] = struct{}{}
			}
		}
	}
	return nil
}

// EraseSubject drops the subject's claims across all its sessions; a ref
// another subject still claims stays.
func (s *MemoryOutputs) EraseSubject(_ context.Context, tenant, subject string) error {
	owner := types.SessionOwner{Tenant: tenant, Subject: subject}
	s.mu.Lock()
	defer s.mu.Unlock()
	for ref := range s.subject[owner] {
		claims := s.claims[ref]
		for claim := range claims {
			if claim.owner == owner {
				delete(claims, claim)
			}
		}
		s.dropIfUnclaimed(ref, claims)
	}
	delete(s.subject, owner)
	return nil
}

// dropIfUnclaimed removes a ref with no claims left, from the blob map and
// every subject index that names it.
func (s *MemoryOutputs) dropIfUnclaimed(ref string, claims map[outputClaim]struct{}) {
	if len(claims) > 0 {
		return
	}
	delete(s.claims, ref)
	delete(s.blobs, ref)
	for _, refs := range s.subject {
		delete(refs, ref)
	}
}

package gohan

import "strings"

// IdentityFieldMatcher reports whether a tool argument or input field name
// looks like an identity field. Core owns the mechanism only: the pattern
// list is a policy value supplied by the caller (std installs the defaults),
// so builds without the defaults keep no identity exclusion.
type IdentityFieldMatcher struct {
	names map[string]struct{}
}

// NewIdentityFieldMatcher matches the given field names case-insensitively.
func NewIdentityFieldMatcher(names ...string) *IdentityFieldMatcher {
	m := &IdentityFieldMatcher{names: make(map[string]struct{}, len(names))}
	for _, n := range names {
		m.names[strings.ToLower(n)] = struct{}{}
	}
	return m
}

// Match reports whether name is one of the configured identity fields.
func (m *IdentityFieldMatcher) Match(name string) bool {
	if m == nil {
		return false
	}
	_, ok := m.names[strings.ToLower(name)]
	return ok
}

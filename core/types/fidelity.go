package types

// Fidelity records how an adapter carries one block kind across the
// provider boundary: intact, converted deterministically, or dropped.
type Fidelity int

const (
	Preserved Fidelity = iota
	Degraded
	Dropped
)

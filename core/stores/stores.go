package stores

// Stores is the value type carrying the store ports a built stack runs
// against. It is data, not a constructor: zero fields mean the port is
// absent and the operation that needs it is refused.
type Stores struct {
	SessionLog SessionLog
}

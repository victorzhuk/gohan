package types

import "context"

type Redactor interface {
	Redact(ctx context.Context, blocks []Block, p RedactionPolicy) ([]Block, RedactionMap, error)
	Rehydrate(ctx context.Context, blocks []Block, m RedactionMap) ([]Block, error)
}

type RedactionPolicy struct {
	Detectors []Detector
	Mode      TokenMode
	Preserve  []EntityKind
}

type TokenMode int

const (
	HMACToken TokenMode = iota
	FormatPreserving
	Vaulted
)

type EntityKind string

const (
	EntityPerson EntityKind = "person"
	EntityEmail  EntityKind = "email"
	EntityPhone  EntityKind = "phone"
	EntityCard   EntityKind = "card"
	EntityIBAN   EntityKind = "iban"
	EntityCustom EntityKind = "custom"
)

type Entity struct {
	Kind  EntityKind
	Value string
}

type Span struct {
	Block int
	Start int
	End   int
	Kind  EntityKind
}

type Detector interface {
	Detect(ctx context.Context, blocks []Block) ([]Span, error)
}

type RedactionMap struct {
	Tenant  string
	Session string
	Entries map[string]Entity
}

type KeySource interface {
	Key(ctx context.Context, tenant string) ([]byte, error)
}

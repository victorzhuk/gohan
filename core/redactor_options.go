package gohan

import "github.com/victorzhuk/gohan/core/types"

// WithRedactor registers the implementation used by resolved native flows.
func WithRedactor(redactor types.Redactor) Option {
	return func(c *config) error {
		c.redactor = redactor
		return nil
	}
}

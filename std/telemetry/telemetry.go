// Package telemetry carries the convention layer between gohan's canonical
// span and metric vocabulary and the naming a telemetry backend expects.
// Core emits gohan.* keys as the source of truth; a Convention renames them
// per backend, so a rename upstream is a one-line change here and never a
// core edit. The layer is mappings and strings only: it holds no
// OpenTelemetry types, and an adapter such as adapter/otel consumes its
// values.
package telemetry

// Package types defines core types for introspection client v2.0.
package types

import "github.com/st-keller/introspection-client/v2/update"

// DataProvider returns plain data (NOT Component!).
// Library constructs Components internally.
type DataProvider func() interface{}

// UpdateInterval is deprecated: Use update.Interval instead.
// Type alias provided for backward compatibility.
type UpdateInterval = update.Interval

// Deprecated constants - use update.Fast, update.Medium, update.Slow instead.
const (
	Fast   = update.Fast
	Medium = update.Medium
	Slow   = update.Slow
)

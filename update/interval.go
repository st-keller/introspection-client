// Package update defines update intervals for automatic component synchronization.
package update

import "fmt"

// Interval defines automatic sync intervals (prime numbers for optimal distribution).
type Interval int

const (
	Fast   Interval = 5  // 5s - health checks, critical metrics
	Medium Interval = 23 // 23s - certificates, statistics
	Slow   Interval = 59 // 59s - logs, connectivity, background data
)

// Seconds returns interval in seconds. Panics on invalid value.
func (i Interval) Seconds() int {
	switch i {
	case Fast:
		return 5
	case Medium:
		return 23
	case Slow:
		return 59
	default:
		panic(fmt.Sprintf("invalid update.Interval: %d (must be Fast/Medium/Slow)", i))
	}
}

// String returns string representation.
func (i Interval) String() string {
	switch i {
	case Fast:
		return "Fast(5s)"
	case Medium:
		return "Medium(23s)"
	case Slow:
		return "Slow(59s)"
	default:
		return fmt.Sprintf("Invalid(%d)", i)
	}
}

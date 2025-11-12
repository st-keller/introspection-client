// Package introspection provides the Go client library for platform introspection (ADR-032).
//
// This library implements the complete introspection protocol with four independent systems:
//   1. Heartbeat System - Ensures service liveness (59s fixed interval, idle_since tracking)
//   2. Update System - Manages component data freshness (dynamic timer for Fast/Medium/Slow)
//   3. Sync System - Efficient transmission via Three-Phase Protocol + continuous reconciliation
//   4. Backoff System - Handles introspection unavailability (prime number sequence)
//
// Architecture: Services provide data, library handles ALL protocol complexity.
//
// Version: 2.5.0 (complete rewrite based on ADR-032)
package introspection

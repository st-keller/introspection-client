// Package standard provides standard component implementations.
package standard

import (
	"sort"
	"sync"
	"time"

)

// ConnectionCall represents a single call to a remote service.
type ConnectionCall struct {
	Timestamp time.Time
	Success   bool
	Latency   time.Duration
	Error     string
}

// Connection tracks connectivity to a single remote service.
type Connection struct {
	Service string
	URL     string
	calls   []ConnectionCall
	mu      sync.Mutex
}

// ConnectivityTracker tracks connectivity to multiple services.
type ConnectivityTracker struct {
	mu          sync.Mutex
	connections map[string]*Connection
}

// NewConnectivityTracker creates a new connectivity tracker.
func NewConnectivityTracker() *ConnectivityTracker {
	return &ConnectivityTracker{
		connections: make(map[string]*Connection),
	}
}

// TrackSuccess records a successful call (data-driven: just pass service, URL, latency!).
func (t *ConnectivityTracker) TrackSuccess(service, url string, latency time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()

	conn := t.getOrCreateConnection(service, url)
	conn.mu.Lock()
	defer conn.mu.Unlock()

	conn.calls = append(conn.calls, ConnectionCall{
		Timestamp: time.Now().UTC(),
		Success:   true,
		Latency:   latency,
	})

	// Keep only last hour
	t.pruneOldCalls(conn)
}

// TrackFailure records a failed call (data-driven: just pass service, URL, latency, error!).
func (t *ConnectivityTracker) TrackFailure(service, url string, latency time.Duration, errorMsg string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	conn := t.getOrCreateConnection(service, url)
	conn.mu.Lock()
	defer conn.mu.Unlock()

	conn.calls = append(conn.calls, ConnectionCall{
		Timestamp: time.Now().UTC(),
		Success:   false,
		Latency:   latency,
		Error:     errorMsg,
	})

	// Keep only last hour
	t.pruneOldCalls(conn)
}

// getOrCreateConnection returns existing connection or creates new one.
func (t *ConnectivityTracker) getOrCreateConnection(service, url string) *Connection {
	if conn, exists := t.connections[service]; exists {
		return conn
	}

	conn := &Connection{
		Service: service,
		URL:     url,
		calls:   make([]ConnectionCall, 0),
	}
	t.connections[service] = conn
	return conn
}

// pruneOldCalls removes calls older than 1 hour.
func (t *ConnectivityTracker) pruneOldCalls(conn *Connection) {
	oneHourAgo := time.Now().Add(-1 * time.Hour)
	for i, call := range conn.calls {
		if call.Timestamp.After(oneHourAgo) {
			conn.calls = conn.calls[i:]
			return
		}
	}
	conn.calls = []ConnectionCall{}
}

// ToComponent converts ConnectivityTracker to a Component (data-driven!).
func (t *ConnectivityTracker) GetData() interface{} {
	t.mu.Lock()
	defer t.mu.Unlock()

	outboundConnections := make([]map[string]interface{}, 0)

	for _, conn := range t.connections {
		conn.mu.Lock()

		if len(conn.calls) == 0 {
			conn.mu.Unlock()
			continue
		}

		// Calculate stats
		var successCount, totalCount int
		var lastCall time.Time
		latencies := make([]float64, 0)
		recentErrors := make([]string, 0)

		for _, call := range conn.calls {
			totalCount++
			if call.Success {
				successCount++
			} else if len(recentErrors) < 5 {
				recentErrors = append(recentErrors, call.Error)
			}

			latencies = append(latencies, float64(call.Latency.Milliseconds()))

			if call.Timestamp.After(lastCall) {
				lastCall = call.Timestamp
			}
		}

		successRate := float64(successCount) / float64(totalCount)

		// Calculate percentiles
		sort.Float64s(latencies)
		p50 := percentile(latencies, 0.50)
		p95 := percentile(latencies, 0.95)
		p99 := percentile(latencies, 0.99)

		// Determine status
		status := "healthy"
		if successRate < 0.9 {
			status = "unhealthy"
		} else if successRate < 0.95 {
			status = "degraded"
		}

		outboundConnections = append(outboundConnections, map[string]interface{}{
			"service":           conn.Service,
			"url":               conn.URL,
			"status":            status,
			"last_call":         lastCall.Format(time.RFC3339),
			"total_calls_1h":    totalCount,
			"success_rate_1h":   successRate,
			"latency_ms": map[string]interface{}{
				"p50": int(p50),
				"p95": int(p95),
				"p99": int(p99),
			},
			"recent_errors": recentErrors,
		})

		conn.mu.Unlock()
	}

	data := map[string]interface{}{
		"outbound_connections": outboundConnections,
	}

	return data
}

// percentile calculates the percentile of a sorted slice.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	index := int(float64(len(sorted)-1) * p)
	return sorted[index]
}

// Package standard provides standard component implementations.
package standard

import (
	"log"
	"sync"
	"time"
)

// LogLevel represents the severity of a log entry.
type LogLevel string

const (
	LevelError LogLevel = "ERROR"
	LevelWarn  LogLevel = "WARN"
	LevelInfo  LogLevel = "INFO"
	LevelDebug LogLevel = "DEBUG"
)

// LogEntry represents a single log entry.
type LogEntry struct {
	Timestamp time.Time              `json:"timestamp"`
	Level     LogLevel               `json:"level"`
	Message   string                 `json:"message"`
	Context   map[string]interface{} `json:"context,omitempty"`
}

// RecentLogs tracks recent log messages.
type RecentLogs struct {
	mu          sync.Mutex
	entries     []LogEntry
	maxEntries  int
	triggerFunc func() // Called on Error/Warn to trigger immediate sync
}

// NewRecentLogs creates a new RecentLogs tracker.
func NewRecentLogs(maxEntries int) *RecentLogs {
	if maxEntries <= 0 {
		maxEntries = 100
	}
	return &RecentLogs{
		entries:     make([]LogEntry, 0, maxEntries),
		maxEntries:  maxEntries,
		triggerFunc: nil,
	}
}

// SetTriggerFunc sets the function to call on Error/Warn (for immediate sync).
func (r *RecentLogs) SetTriggerFunc(fn func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.triggerFunc = fn
}

// Log adds a log entry with context (data-driven: pass level + message + context!).
// Context must be non-empty to ensure structured logging.
// IMPORTANT: Also logs to stdout/journald for visibility!
func (r *RecentLogs) Log(level LogLevel, message string, context map[string]interface{}) {
	// Validate: context must not be empty
	if len(context) == 0 {
		panic("RecentLogs.Log: context must be non-empty (use structured logging!)")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	entry := LogEntry{
		Timestamp: time.Now().UTC(),
		Level:     level,
		Message:   message,
		Context:   context,
	}

	r.entries = append(r.entries, entry)

	// Keep only last N entries (ringbuffer)
	if len(r.entries) > r.maxEntries {
		r.entries = r.entries[len(r.entries)-r.maxEntries:]
	}

	// CRITICAL: Also log to stdout/journald for visibility!
	// This ensures logs appear in journalctl, not just in introspection
	log.Printf("[%s] %s %v", level, message, context)
}

// Error logs an error message with context.
// Triggers immediate sync if triggerFunc is set (Error/Warn = critical!).
func (r *RecentLogs) Error(message string, context map[string]interface{}) {
	r.Log(LevelError, message, context)

	// Trigger immediate sync on ERROR
	r.mu.Lock()
	triggerFunc := r.triggerFunc
	r.mu.Unlock()

	if triggerFunc != nil {
		triggerFunc()
	}
}

// Warn logs a warning message with context.
// Triggers immediate sync if triggerFunc is set (Error/Warn = critical!).
func (r *RecentLogs) Warn(message string, context map[string]interface{}) {
	r.Log(LevelWarn, message, context)

	// Trigger immediate sync on WARN
	r.mu.Lock()
	triggerFunc := r.triggerFunc
	r.mu.Unlock()

	if triggerFunc != nil {
		triggerFunc()
	}
}

// Info logs an info message with context.
func (r *RecentLogs) Info(message string, context map[string]interface{}) {
	r.Log(LevelInfo, message, context)
}

// Debug logs a debug message with context.
func (r *RecentLogs) Debug(message string, context map[string]interface{}) {
	r.Log(LevelDebug, message, context)
}

// WarnNoTrigger logs a warning WITHOUT triggering sync (for internal library logs).
// Use this to avoid feedback loops when logging sync failures!
func (r *RecentLogs) WarnNoTrigger(message string, context map[string]interface{}) {
	r.Log(LevelWarn, message, context)
	// NO trigger - prevents feedback loop!
}

// ErrorNoTrigger logs an error WITHOUT triggering sync (for internal library logs).
// Use this to avoid feedback loops when logging sync failures!
func (r *RecentLogs) ErrorNoTrigger(message string, context map[string]interface{}) {
	r.Log(LevelError, message, context)
	// NO trigger - prevents feedback loop!
}

// GetData returns log data for introspection (v2.0: returns plain data, NOT Component!).
func (r *RecentLogs) GetData() interface{} {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Calculate stats inline (avoid double-locking)
	var errorCount, warnCount, infoCount, debugCount int
	for _, entry := range r.entries {
		switch entry.Level {
		case LevelError:
			errorCount++
		case LevelWarn:
			warnCount++
		case LevelInfo:
			infoCount++
		case LevelDebug:
			debugCount++
		}
	}

	return map[string]interface{}{
		"entries": r.entries,
		"stats": map[string]interface{}{
			"total_count":    len(r.entries),
			"errors_count":   errorCount,
			"warnings_count": warnCount,
			"info_count":     infoCount,
			"debug_count":    debugCount,
			"max_entries":    r.maxEntries,
		},
	}
}

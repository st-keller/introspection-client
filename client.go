// Package introspection provides the Go client library for platform introspection (ADR-032).
//
// This library implements the complete introspection protocol with four independent systems:
//   1. Heartbeat System - Ensures service liveness (59s fixed interval, idle_since tracking)
//   2. Update System - Manages component data freshness (dynamic timer for Fast/Medium/Slow)
//   3. Sync System - Efficient transmission via Three-Phase Protocol + continuous reconciliation
//   4. Backoff System - Handles introspection unavailability (prime number sequence)
//
// Architecture: Services provide data, library handles ALL protocol complexity.
// Standard Components: Automatically registered (service-info, logs, connectivity, certificates).
//
// Version: 2.5.0 (complete rewrite based on ADR-032)
package introspection

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/st-keller/introspection-client/v2/component"
	"github.com/st-keller/introspection-client/v2/registry"
	"github.com/st-keller/introspection-client/v2/standard"
	"github.com/st-keller/introspection-client/v2/transport"
	"github.com/st-keller/introspection-client/v2/types"
	"github.com/st-keller/introspection-client/v2/update"
)

// HeartbeatIntervalSec is the fixed heartbeat interval for ALL services (ADR-032).
const HeartbeatIntervalSec = 59

// Config holds client configuration (NO DEFAULTS - all required!).
type Config struct {
	ServiceName      string // Service name (e.g., "ca-manager")
	Version          string // Service version (e.g., "1.0.0")
	Port             int    // Service port (e.g., 8443)
	Server           string // Server name: "staging" or "production"
	IntrospectionURL string // Introspection service URL (e.g., "https://introspection:9080")
	CertPath         string // Path to client certificate
	KeyPath          string // Path to client key
	CAPath           string // Path to CA certificate
	CertDir          string // Directory containing *.cert.pem files for monitoring
}

// Validate checks if all required config fields are present.
func (c Config) Validate() error {
	if c.ServiceName == "" {
		return fmt.Errorf("ServiceName required")
	}
	if c.Version == "" {
		return fmt.Errorf("Version required")
	}
	if c.Port <= 0 {
		return fmt.Errorf("Port required (must be > 0)")
	}
	if c.Server == "" {
		return fmt.Errorf("Server required (staging or production)")
	}
	if c.IntrospectionURL == "" {
		return fmt.Errorf("IntrospectionURL required")
	}
	if c.CertPath == "" {
		return fmt.Errorf("CertPath required")
	}
	if c.KeyPath == "" {
		return fmt.Errorf("KeyPath required")
	}
	if c.CAPath == "" {
		return fmt.Errorf("CAPath required")
	}
	if c.CertDir == "" {
		return fmt.Errorf("CertDir required")
	}
	return nil
}

// Client is the introspection client implementing ADR-032.
type Client struct {
	config   Config
	entityID string // Own entity ID: "serviceName-serverName"
	registry *registry.Registry
	http     *http.Client

	// Standard components (auto-registered, public access via getters)
	logs         *standard.RecentLogs
	connectivity *standard.ConnectivityTracker
	certMonitor  *standard.CertificateMonitor

	// System state
	mu       sync.Mutex
	running  bool
	stopChan chan struct{}

	// Heartbeat System state
	idleSince      time.Time // Last real activity (non-heartbeat sync)
	heartbeatTimer *time.Timer

	// Update System state
	updateTimer *time.Timer

	// Backoff System state
	backoffIndex int // Current position in prime sequence

	// Sync System state
	syncMu      sync.Mutex // Protects sync execution (only one sync at a time)
	syncPending bool       // True if sync needs to run after current sync completes
}

// New creates a new introspection client with auto-registered standard components.
func New(config Config) (*Client, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	// Build entity ID
	entityID := fmt.Sprintf("%s-%s", config.ServiceName, config.Server)

	// Create registry
	reg := registry.New(entityID)

	// Create HTTP/2 client with mTLS 1.3
	httpClient, err := transport.BuildHTTP2Client(config.CertPath, config.KeyPath, config.CAPath)
	if err != nil {
		return nil, fmt.Errorf("failed to build HTTP client: %w", err)
	}

	// Create standard components
	logs := standard.NewRecentLogs(100)
	connectivity := standard.NewConnectivityTracker()
	certMonitor := standard.NewCertificateMonitor(config.CertDir)

	client := &Client{
		config:       config,
		entityID:     entityID,
		registry:     reg,
		http:         httpClient,
		logs:         logs,
		connectivity: connectivity,
		certMonitor:  certMonitor,
		stopChan:     make(chan struct{}),
		idleSince:    time.Now(), // Service just started = activity!
		backoffIndex: 0,
	}

	// Auto-register standard components (NO OPT-OUT!)
	if err := client.registerStandardComponents(); err != nil {
		return nil, fmt.Errorf("failed to register standard components: %w", err)
	}

	// Initial logs go to stdout only (logs not initialized yet)
	log.Printf("✅ Introspection client initialized (entity: %s, service: %s v%s)", entityID, c.config.ServiceName, c.config.Version)
	log.Printf("   📦 Auto-registered: service-info (static), recent-logs (59s), connectivity (59s), certificates (trigger)")

	return client, nil
}

// registerStandardComponents auto-registers all standard components.
func (c *Client) registerStandardComponents() error {
	// 1. service-info (OnlyTrigger - static data)
	serviceInfo := standard.AutoDetect(c.config.ServiceName, c.config.Version, c.config.Port)
	if err := c.registry.Register("service-info", serviceInfo.GetData); err != nil {
		return err
	}

	// 2. recent-logs (Slow = 59s)
	if err := c.registry.Register("recent-logs", c.logs.GetData, update.Slow); err != nil {
		return err
	}

	// Set trigger function for Error/Warn (immediate sync)
	c.logs.SetTriggerFunc(func() {
		// Non-blocking trigger
		go c.triggerSyncFromLogs()
	})

	// 3. inter-service-connectivity (Slow = 59s)
	if err := c.registry.Register("inter-service-connectivity", c.connectivity.GetData, update.Slow); err != nil {
		return err
	}

	// 4. certificates (OnlyTrigger - scan on demand when certs change)
	if err := c.registry.Register("certificates", func() interface{} {
		// Scan filesystem on every collection
		if err := c.certMonitor.Scan(); err != nil {
			c.logs.Warn("Certificate scan failed", map[string]interface{}{
				"error": err.Error(),
			})
		}
		return c.certMonitor.GetData()
	}); err != nil { // No update interval - OnlyTrigger
		return err
	}

	return nil
}

// triggerSyncFromLogs triggers sync when Error/Warn logged (called from logs component).
func (c *Client) triggerSyncFromLogs() {
	// This is REAL ACTIVITY → reset idle_since + heartbeat timer
	c.mu.Lock()
	c.idleSince = time.Now()
	c.mu.Unlock()
	c.resetHeartbeatTimer()

	// Trigger sync
	c.triggerSync("logs:error-or-warn")
}

// GetLogs returns the logs component for service logging.
func (c *Client) GetLogs() *standard.RecentLogs {
	return c.logs
}

// GetConnectivity returns the connectivity tracker for manual call tracking.
func (c *Client) GetConnectivity() *standard.ConnectivityTracker {
	return c.connectivity
}

// GetCertMonitor returns the certificate monitor for expiry checking.
func (c *Client) GetCertMonitor() *standard.CertificateMonitor {
	return c.certMonitor
}

// Register registers a custom component for the own entity.
// updateInterval is optional: omit = OnlyTrigger, update.Fast/Medium/Slow = periodic updates
func (c *Client) Register(componentID string, provider types.DataProvider, updateInterval ...update.Interval) error {
	return c.registry.Register(componentID, provider, updateInterval...)
}

// RegisterForEntity registers a component for another entity (multi-entity support).
// updateInterval is optional: omit = OnlyTrigger, update.Fast/Medium/Slow = periodic updates
func (c *Client) RegisterForEntity(entityID, componentID string, provider types.DataProvider, updateInterval ...update.Interval) error {
	return c.registry.RegisterForEntity(entityID, componentID, provider, updateInterval...)
}

// TriggerUpdate triggers an immediate update for a component (Update System).
// ADR-032: This collects data SYNCHRONOUSLY (calls provider()), then triggers async sync.
func (c *Client) TriggerUpdate(componentID string) error {
	return c.TriggerUpdateForEntity(c.entityID, componentID)
}

// TriggerUpdateForEntity triggers update for a component of any entity (multi-entity).
func (c *Client) TriggerUpdateForEntity(entityID, componentID string) error {
	// SYNCHRONOUS: Collect component data NOW (calls provider())
	_, err := c.registry.Collect(entityID, componentID)
	if err != nil {
		return fmt.Errorf("failed to collect %s/%s: %w", entityID, componentID, err)
	}

	// This is REAL ACTIVITY (not heartbeat) → reset idle_since + heartbeat timer
	c.mu.Lock()
	c.idleSince = time.Now()
	c.mu.Unlock()
	c.resetHeartbeatTimer()

	// ASYNCHRONOUS: Trigger sync in background
	go c.triggerSync("trigger:" + componentID)

	return nil
}

// Start starts the background systems (Heartbeat, Update, Sync).
func (c *Client) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.running {
		return fmt.Errorf("client already running")
	}

	c.running = true

	// Start Heartbeat System (timer-based)
	c.startHeartbeatSystem()

	// Start Update System (timer-based)
	c.startUpdateSystem()

	// Startup complete - can now use logs component
	c.logs.Info("Introspection client started", map[string]interface{}{
		"heartbeat_interval_sec": HeartbeatIntervalSec,
	})

	return nil
}

// Stop gracefully stops the client.
func (c *Client) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.running {
		return
	}

	c.running = false
	close(c.stopChan)

	// Stop timers
	if c.heartbeatTimer != nil {
		c.heartbeatTimer.Stop()
	}
	if c.updateTimer != nil {
		c.updateTimer.Stop()
	}

	c.logs.Info("Introspection client stopped", map[string]interface{}{
		"entity_id": c.entityID,
	})
}

// ============================================================================
// HEARTBEAT SYSTEM (ADR-032: Section "1. Heartbeat System")
// ============================================================================

// startHeartbeatSystem initializes the heartbeat timer.
func (c *Client) startHeartbeatSystem() {
	interval := time.Duration(HeartbeatIntervalSec) * time.Second
	c.heartbeatTimer = time.AfterFunc(interval, c.onHeartbeatFire)
}

// onHeartbeatFire is called when heartbeat timer fires.
func (c *Client) onHeartbeatFire() {
	// Check if still running
	c.mu.Lock()
	if !c.running {
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()

	// ADR-032: Heartbeat does NOT reset idle_since!
	// idle_since stays unchanged - this indicates "I'm idle since X"

	// Trigger sync (heartbeat is just another sync trigger)
	go c.triggerSync("heartbeat-timer")

	// Reset timer for next heartbeat
	interval := time.Duration(HeartbeatIntervalSec) * time.Second
	c.heartbeatTimer.Reset(interval)
}

// resetHeartbeatTimer resets the heartbeat timer (called on real activity).
func (c *Client) resetHeartbeatTimer() {
	interval := time.Duration(HeartbeatIntervalSec) * time.Second
	c.mu.Lock()
	if c.heartbeatTimer != nil {
		c.heartbeatTimer.Reset(interval)
	}
	c.mu.Unlock()
}

// ============================================================================
// UPDATE SYSTEM (ADR-032: Section "2. Component Update System")
// ============================================================================

// startUpdateSystem initializes the dynamic update timer.
func (c *Client) startUpdateSystem() {
	c.scheduleNextUpdate()
}

// scheduleNextUpdate calculates when the next component update is due.
func (c *Client) scheduleNextUpdate() {
	nextUpdate := c.registry.GetNextUpdateTime()

	if nextUpdate.IsZero() {
		// No components with updateInterval - no timer needed
		return
	}

	duration := time.Until(nextUpdate)
	if duration < 0 {
		duration = 0 // Already due - fire immediately
	}

	c.updateTimer = time.AfterFunc(duration, c.onUpdateTimerFire)
}

// onUpdateTimerFire is called when update timer fires.
func (c *Client) onUpdateTimerFire() {
	// Check if still running
	c.mu.Lock()
	if !c.running {
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()

	// Get all components that need update (maxAge exceeded)
	dueComponents := c.registry.GetDueComponents()

	if len(dueComponents) > 0 {
		// Collect data for due components (calls provider())
		for entityID, componentIDs := range dueComponents {
			for _, componentID := range componentIDs {
				_, err := c.registry.Collect(entityID, componentID)
				if err != nil {
					log.Printf("⚠️  Failed to collect %s/%s: %v", entityID, componentID, err)
				}
			}
		}

		// Trigger sync in background (Update System does NOT reset idle_since!)
		go c.triggerSync("update-timer")
	}

	// Schedule next update (dynamic timer)
	c.scheduleNextUpdate()
}

// ============================================================================
// SYNC SYSTEM (ADR-032: Section "3. Intelligent Sync System")
// ============================================================================

// triggerSync triggers a sync execution.
// If sync is already running, marks syncPending=true to run again after completion.
func (c *Client) triggerSync(source string) {
	c.syncMu.Lock()

	// Check if sync already running
	if c.syncPending {
		// Already pending - no need to mark again
		c.syncMu.Unlock()
		return
	}

	c.syncPending = true
	c.syncMu.Unlock()

	// Run sync loop (handles pending flag internally)
	c.executeSyncLoop(source)
}

// executeSyncLoop runs syncs while syncPending=true.
func (c *Client) executeSyncLoop(source string) {
	for {
		c.syncMu.Lock()
		if !c.syncPending {
			c.syncMu.Unlock()
			return
		}
		c.syncPending = false
		c.syncMu.Unlock()

		// Execute sync with backoff
		c.executeSync(source)
	}
}

// executeSync performs the Three-Phase Sync Protocol with exponential backoff.
func (c *Client) executeSync(source string) {
	// Retry loop with exponential backoff (prime numbers)
	for {
		err := c.performThreePhaseSync()
		if err == nil {
			// Success! Reset backoff
			c.mu.Lock()
			c.backoffIndex = 0
			c.mu.Unlock()
			return
		}

		// Failure - apply backoff
		c.mu.Lock()
		backoffDuration := c.getBackoffDuration()
		c.backoffIndex++
		c.mu.Unlock()

		// Use ErrorNoTrigger to avoid feedback loop (sync fails → log → trigger sync → ...)
		c.logs.ErrorNoTrigger("Sync failed, retrying with backoff", map[string]interface{}{
			"source":         source,
			"error":          err.Error(),
			"backoff_sec":    backoffDuration.Seconds(),
			"retry_in":       backoffDuration.String(),
		})
		time.Sleep(backoffDuration)
	}
}

// performThreePhaseSync executes the Three-Phase Sync Protocol (ADR-028).
func (c *Client) performThreePhaseSync() error {
	// === PHASE 1: Collect ALL component checksums ===
	allRegistered := c.registry.GetAllRegistered()
	checksums := make(map[string]map[string]string) // entityID -> componentID -> checksum

	for entityID, componentIDs := range allRegistered {
		checksums[entityID] = make(map[string]string)
		for _, componentID := range componentIDs {
			comp, err := c.registry.Collect(entityID, componentID)
			if err != nil {
				c.logs.Warn("Failed to collect component for update check", map[string]interface{}{
					"entity_id":    entityID,
					"component_id": componentID,
					"error":        err.Error(),
				})
				continue
			}
			checksums[entityID][componentID] = comp.Checksum
		}
	}

	// Build heartbeat component
	c.mu.Lock()
	idleSince := c.idleSince
	c.mu.Unlock()

	// Format timestamps as RFC3339 (without nanoseconds) for consistency
	now := time.Now().UTC()
	heartbeatData := map[string]interface{}{
		"heartbeat":  now.Format("2006-01-02T15:04:05+00:00"),        // Current heartbeat timestamp
		"idle_since": idleSince.Format("2006-01-02T15:04:05+00:00"),  // Last real activity timestamp
	}
	heartbeatComp := component.New("heartbeat", heartbeatData)

	// Add heartbeat to checksums
	if checksums[c.entityID] == nil {
		checksums[c.entityID] = make(map[string]string)
	}
	checksums[c.entityID]["heartbeat"] = heartbeatComp.Checksum

	// === PHASE 2: Send checksums, receive needed component IDs ===
	payload := map[string]interface{}{
		"service":   c.config.ServiceName,
		"server":    c.config.Server,
		"checksums": checksums,
	}

	neededComponents, err := c.sendChecksums(payload)
	if err != nil {
		return fmt.Errorf("checksum phase failed: %w", err)
	}

	// === PHASE 3: Send only needed components ===
	if len(neededComponents) > 0 {
		componentsToSend := make(map[string][]component.Component)

		for entityID, componentIDs := range neededComponents {
			for _, componentID := range componentIDs {
				var comp component.Component
				if componentID == "heartbeat" && entityID == c.entityID {
					comp = heartbeatComp
				} else {
					var err error
					comp, err = c.registry.Collect(entityID, componentID)
					if err != nil {
						c.logs.Warn("Failed to collect component for sync", map[string]interface{}{
							"entity_id":    entityID,
							"component_id": componentID,
							"error":        err.Error(),
						})
						continue
					}
				}

				if componentsToSend[entityID] == nil {
					componentsToSend[entityID] = []component.Component{}
				}
				componentsToSend[entityID] = append(componentsToSend[entityID], comp)
			}
		}

		err = c.sendComponents(componentsToSend)
		if err != nil {
			return fmt.Errorf("data phase failed: %w", err)
		}
	}

	return nil
}

// sendChecksums sends checksums to introspection (Phase 1).
// Returns map of entityID -> []componentID that introspection needs.
func (c *Client) sendChecksums(payload map[string]interface{}) (map[string][]string, error) {
	url := c.config.IntrospectionURL + "/sync/checksums"

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal checksums: %w", err)
	}

	// Track connectivity (start timer)
	startTime := time.Now()

	resp, err := c.http.Post(url, "application/json", bytes.NewReader(jsonData))
	latency := time.Since(startTime)

	if err != nil {
		// Track failed request
		c.connectivity.TrackFailure("introspection", c.config.IntrospectionURL, latency, err.Error())
		c.logs.ErrorNoTrigger("Introspection sync failed", map[string]interface{}{
			"phase":      "checksums",
			"error":      err.Error(),
			"latency_ms": latency.Milliseconds(),
		})
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		errorMsg := fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body))
		// Track failed request
		c.connectivity.TrackFailure("introspection", c.config.IntrospectionURL, latency, errorMsg)
		c.logs.ErrorNoTrigger("Introspection sync failed", map[string]interface{}{
			"phase":      "checksums",
			"status":     resp.StatusCode,
			"error":      string(body),
			"latency_ms": latency.Milliseconds(),
		})
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var response struct {
		Needed map[string][]string `json:"needed"` // entityID -> []componentID
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		// Track successful HTTP but failed decode
		c.connectivity.TrackSuccess("introspection", c.config.IntrospectionURL, latency)
		c.logs.ErrorNoTrigger("Failed to decode introspection response", map[string]interface{}{
			"phase":      "checksums",
			"error":      err.Error(),
			"latency_ms": latency.Milliseconds(),
		})
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Track successful request
	c.connectivity.TrackSuccess("introspection", c.config.IntrospectionURL, latency)

	return response.Needed, nil
}

// sendComponents sends component data to introspection (Phase 3).
func (c *Client) sendComponents(components map[string][]component.Component) error {
	url := c.config.IntrospectionURL + "/sync/components"

	payload := map[string]interface{}{
		"service":    c.config.ServiceName,
		"server":     c.config.Server,
		"components": components,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal components: %w", err)
	}

	// Track connectivity (start timer)
	startTime := time.Now()

	resp, err := c.http.Post(url, "application/json", bytes.NewReader(jsonData))
	latency := time.Since(startTime)

	if err != nil {
		// Track failed request
		c.connectivity.TrackFailure("introspection", c.config.IntrospectionURL, latency, err.Error())
		c.logs.ErrorNoTrigger("Introspection sync failed", map[string]interface{}{
			"phase":      "components",
			"error":      err.Error(),
			"latency_ms": latency.Milliseconds(),
		})
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		errorMsg := fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body))
		// Track failed request
		c.connectivity.TrackFailure("introspection", c.config.IntrospectionURL, latency, errorMsg)
		c.logs.ErrorNoTrigger("Introspection sync failed", map[string]interface{}{
			"phase":      "components",
			"status":     resp.StatusCode,
			"error":      string(body),
			"latency_ms": latency.Milliseconds(),
		})
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	// Track successful request
	c.connectivity.TrackSuccess("introspection", c.config.IntrospectionURL, latency)

	return nil
}

// ============================================================================
// BACKOFF SYSTEM (ADR-032: Section "4. Exponential Backoff System")
// ============================================================================

// Prime number sequence for backoff (ADR-032).
var backoffPrimes = []int{1, 2, 3, 5, 11, 23, 47, 61}

// getBackoffDuration returns the current backoff duration.
func (c *Client) getBackoffDuration() time.Duration {
	// Cap at heartbeat interval (59s)
	maxBackoff := HeartbeatIntervalSec

	index := c.backoffIndex
	if index >= len(backoffPrimes) {
		// Exhausted primes - use max backoff
		return time.Duration(maxBackoff) * time.Second
	}

	backoffSec := backoffPrimes[index]
	if backoffSec > maxBackoff {
		backoffSec = maxBackoff
	}

	return time.Duration(backoffSec) * time.Second
}

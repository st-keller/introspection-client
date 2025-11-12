// Package registry implements component registration with smart caching.
package registry

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/st-keller/introspection-client/v2/component"
	"github.com/st-keller/introspection-client/v2/types"
	"github.com/st-keller/introspection-client/v2/update"
)

// Registry manages component providers with multi-entity support.
type Registry struct {
	mu sync.RWMutex

	// ownEntityID is the entity ID of the service itself
	ownEntityID string

	// configs: entityID -> componentID -> ComponentConfig
	configs map[string]map[string]*ComponentConfig

	// cache: entityID -> componentID -> CachedComponent
	cache map[string]map[string]*CachedComponent
}

// ComponentConfig holds provider and update settings.
type ComponentConfig struct {
	provider       types.DataProvider
	updateInterval *update.Interval // nil = no auto-update
}

// CachedComponent holds cached data for smart checksum optimization.
type CachedComponent struct {
	lastRawJSON   []byte            // JSON for comparison
	lastChecksum  string            // SHA256 checksum
	lastComponent component.Component // Cached component
	lastSync      time.Time         // Last sync time (when sent to introspection)
	lastUpdate    time.Time         // Last update time (when provider() called)
}

// New creates a new Registry for the given entity.
func New(ownEntityID string) *Registry {
	if ownEntityID == "" {
		panic("ownEntityID required")
	}

	return &Registry{
		ownEntityID: ownEntityID,
		configs:     make(map[string]map[string]*ComponentConfig),
		cache:       make(map[string]map[string]*CachedComponent),
	}
}

// Register registers a component for the own entity.
// updateInterval is optional: omit = OnlyTrigger (no auto-update)
func (r *Registry) Register(componentID string, provider types.DataProvider, updateInterval ...update.Interval) error {
	return r.RegisterForEntity(r.ownEntityID, componentID, provider, updateInterval...)
}

// RegisterForEntity registers a component for any entity (multi-entity support).
// updateInterval is optional: omit = OnlyTrigger (no auto-update)
func (r *Registry) RegisterForEntity(entityID, componentID string, provider types.DataProvider, updateInterval ...update.Interval) error {
	if entityID == "" {
		return fmt.Errorf("entityID required")
	}
	if componentID == "" {
		return fmt.Errorf("componentID required")
	}
	if provider == nil {
		return fmt.Errorf("provider required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Initialize entity maps if needed
	if r.configs[entityID] == nil {
		r.configs[entityID] = make(map[string]*ComponentConfig)
	}
	if r.cache[entityID] == nil {
		r.cache[entityID] = make(map[string]*CachedComponent)
	}

	// Check duplicate
	if r.configs[entityID][componentID] != nil {
		return fmt.Errorf("component %s already registered for entity %s", componentID, entityID)
	}

	// Convert variadic to pointer: nil if omitted, &value if provided
	var intervalPtr *update.Interval
	if len(updateInterval) > 0 {
		intervalPtr = &updateInterval[0]
	}

	r.configs[entityID][componentID] = &ComponentConfig{
		provider:       provider,
		updateInterval: intervalPtr,
	}

	return nil
}

// Collect collects a component with smart checksum caching.
// Returns cached component if data unchanged (no SHA256!).
func (r *Registry) Collect(entityID, componentID string) (component.Component, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	config := r.configs[entityID][componentID]
	if config == nil {
		return component.Component{}, fmt.Errorf("component %s not registered for entity %s", componentID, entityID)
	}

	// Call provider - service returns ONLY data!
	data := config.provider()
	now := time.Now()

	// Serialize to JSON
	jsonData, err := json.Marshal(data)
	if err != nil {
		return component.Component{}, fmt.Errorf("failed to marshal component data: %w", err)
	}

	// Check cache - compare JSON before computing SHA256!
	cached := r.cache[entityID][componentID]
	if cached != nil && bytes.Equal(cached.lastRawJSON, jsonData) {
		// Data unchanged - return cached component (skip SHA256!)
		// But update lastUpdate timestamp (provider was called)
		cached.lastUpdate = now
		return cached.lastComponent, nil
	}

	// Data changed - compute SHA256
	hash := sha256.Sum256(jsonData)
	checksum := hex.EncodeToString(hash[:])

	comp := component.Component{
		ID:       componentID,
		Type:     componentID,
		Checksum: checksum,
		Data:     json.RawMessage(jsonData),
	}

	// Update cache (preserve lastSync if exists, update lastUpdate)
	lastSync := time.Time{}
	if cached != nil {
		lastSync = cached.lastSync
	}

	r.cache[entityID][componentID] = &CachedComponent{
		lastRawJSON:   jsonData,
		lastChecksum:  checksum,
		lastComponent: comp,
		lastSync:      lastSync,
		lastUpdate:    now,
	}

	return comp, nil
}

// GetDueComponents returns component IDs that need update (maxAge exceeded).
// Uses lastUpdate (when provider() was called), not lastSync (when sent to introspection).
func (r *Registry) GetDueComponents() map[string][]string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	due := make(map[string][]string)

	for entityID, entityConfigs := range r.configs {
		for componentID, config := range entityConfigs {
			// No update interval - skip (OnlyTrigger components)
			if config.updateInterval == nil {
				continue
			}

			cached := r.cache[entityID][componentID]
			maxAge := time.Duration(config.updateInterval.Seconds()) * time.Second

			// Check if update is due based on lastUpdate (not lastSync!)
			if cached == nil || time.Since(cached.lastUpdate) >= maxAge {
				if due[entityID] == nil {
					due[entityID] = []string{}
				}
				due[entityID] = append(due[entityID], componentID)
			}
		}
	}

	return due
}

// GetNextUpdateTime returns when the next component update is due.
// Returns zero time if no components have updateInterval.
func (r *Registry) GetNextUpdateTime() time.Time {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var nextUpdate time.Time

	for entityID, entityConfigs := range r.configs {
		for componentID, config := range entityConfigs {
			// No update interval - skip
			if config.updateInterval == nil {
				continue
			}

			cached := r.cache[entityID][componentID]
			maxAge := time.Duration(config.updateInterval.Seconds()) * time.Second

			var componentNextUpdate time.Time
			if cached == nil || cached.lastUpdate.IsZero() {
				// Never updated - due immediately
				componentNextUpdate = time.Now()
			} else {
				// Next update = lastUpdate + maxAge
				componentNextUpdate = cached.lastUpdate.Add(maxAge)
			}

			// Track earliest next update
			if nextUpdate.IsZero() || componentNextUpdate.Before(nextUpdate) {
				nextUpdate = componentNextUpdate
			}
		}
	}

	return nextUpdate
}

// GetAllRegistered returns all registered component IDs per entity (for ghost detection).
func (r *Registry) GetAllRegistered() map[string][]string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	registered := make(map[string][]string)

	for entityID, entityConfigs := range r.configs {
		ids := make([]string, 0, len(entityConfigs))
		for componentID := range entityConfigs {
			ids = append(ids, componentID)
		}
		registered[entityID] = ids
	}

	return registered
}

// GetOwnEntityID returns the entity ID of the service itself.
func (r *Registry) GetOwnEntityID() string {
	return r.ownEntityID
}

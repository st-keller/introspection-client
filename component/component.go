// Package component provides the core Component type for introspection data.
package component

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// Component represents a single introspection component with content-based checksum (ADR-028).
// This is the ONLY data structure services need to know about!
type Component struct {
	ID       string      `json:"id"`
	Type     string      `json:"type"`
	Checksum string      `json:"checksum"`
	Data     interface{} `json:"data"`
}

// New creates a new component with automatic checksum calculation.
// This is data-driven: pass any JSON-serializable data, get a valid Component.
func New(componentType string, data interface{}) Component {
	jsonData, _ := json.Marshal(data)
	hash := sha256.Sum256(jsonData)
	checksum := hex.EncodeToString(hash[:])

	return Component{
		ID:       componentType,
		Type:     componentType,
		Checksum: checksum,
		Data:     data,
	}
}

// Provider is a function that generates components dynamically.
// Services register providers, library calls them - completely data-driven!
type Provider func() []Component

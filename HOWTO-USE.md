# Using Go Introspection Client Library in New Services

This guide shows you how to integrate the Go introspection client library into a service that **doesn't use the library yet**.

**Current Version:** v2.6.4

---

## TL;DR - Quick Integration Checklist

- [ ] Add library to `go.mod`: `github.com/st-keller/introspection-client/v2 v2.6.4`
- [ ] Create `logging.go` with global instances
- [ ] Create `introspection.go` with manager setup
- [ ] Update `main.go` to initialize introspection
- [ ] Make PORT required (no defaults!)
- [ ] Track all HTTP calls with ConnectivityTracker
- [ ] Use structured logging (LogInfo/LogWarn/LogError)
- [ ] Test & Deploy

---

## Why Use the Library?

**The library gives you:**
- ✅ **Automatic introspection** - Service appears in viewer with metadata
- ✅ **Ghost detection** - Automatic alive/ghost status for services and components
- ✅ **Structured logging** - All logs tracked in `recent-logs` component
- ✅ **Connectivity tracking** - HTTP calls tracked with latency/success rate
- ✅ **Standard components** - Auto-registered (service-info, certificates, heartbeat, etc.)
- ✅ **Zero boilerplate** - Library handles all protocol complexity

---

## Step 1: Add Library Dependency

Update `go.mod`:

```go
require (
    github.com/st-keller/introspection-client/v2 v2.6.4
    // ... other dependencies
)
```

Run:
```bash
go get github.com/st-keller/introspection-client/v2@v2.6.4
go mod tidy
```

---

## Step 2: Create `logging.go`

Create a file that exposes global logging and connectivity tracking:

```go
package main

import (
	"github.com/st-keller/introspection-client/v2/standard"
)

// Global instances (shared across entire service)
var (
	globalRecentLogs          *standard.RecentLogs
	globalConnectivityTracker *standard.ConnectivityTracker
)

// Structured logging helpers - ALL logs go to RecentLogs component
// If a log is important enough to be in code, it's important enough to track!
// Always use structured logging with context - NEVER log without context!

func LogInfo(message string, context map[string]interface{}) {
	if globalRecentLogs != nil {
		globalRecentLogs.Info(message, context)
	}
}

func LogWarn(message string, context map[string]interface{}) {
	if globalRecentLogs != nil {
		globalRecentLogs.Warn(message, context)
	}
}

func LogError(message string, context map[string]interface{}) {
	if globalRecentLogs != nil {
		globalRecentLogs.Error(message, context)
	}
}
```

**Important:**
- Always use structured logging with `context` map
- Library logs to BOTH introspection AND stdout/journald
- Error/Warn trigger immediate sync (critical logs!)

---

## Step 3: Create `introspection.go`

Create introspection manager setup:

```go
package main

import (
	"log"
	"os"
	"strings"
	"time"

	introspection "github.com/st-keller/introspection-client/v2"
)

// IntrospectionManager manages introspection reporting (ADR-028)
type IntrospectionManager struct {
	client *introspection.Client
}

// NewIntrospectionManager creates a new introspection manager
func NewIntrospectionManager(config *Config, startTime time.Time) (*IntrospectionManager, error) {
	// Read version from VERSION file
	versionData, err := os.ReadFile("VERSION")
	if err != nil {
		return nil, err
	}
	version := strings.TrimSpace(string(versionData))

	// Determine introspection URL
	introspectionURL := os.Getenv("INTROSPECTION_URL")
	if introspectionURL == "" {
		introspectionURL = "https://localhost:9080"
	}

	// Certificate paths (adjust for your service!)
	certPath := os.Getenv("INTROSPECTION_CLIENT_CERT_PATH")
	if certPath == "" {
		certPath = "/certs/YOUR-SERVICE-to-introspection.cert.pem"
	}

	keyPath := os.Getenv("INTROSPECTION_CLIENT_KEY_PATH")
	if keyPath == "" {
		keyPath = "/certs/YOUR-SERVICE-to-introspection.key.pem"
	}

	caPath := os.Getenv("CA_FILE")
	if caPath == "" {
		// Auto-detect which CA file to use
		if _, err := os.Stat("/certs/ca-chain.cert.pem"); err == nil {
			caPath = "/certs/ca-chain.cert.pem"
		} else {
			caPath = "/certs/ca.cert.pem"
		}
	}

	certDir := "/certs"

	// Initialize v2 client (auto-registers standard components!)
	client, err := introspection.New(introspection.Config{
		ServiceName:      "YOUR-SERVICE-NAME",
		Version:          version,
		Port:             config.Port,
		Server:           config.Environment,
		IntrospectionURL: introspectionURL,
		CertPath:         certPath,
		KeyPath:          keyPath,
		CAPath:           caPath,
		CertDir:          certDir,
	})
	if err != nil {
		return nil, err
	}

	// Get auto-registered standard components
	globalRecentLogs = client.GetLogs()
	globalConnectivityTracker = client.GetConnectivity()

	globalRecentLogs.Info("Service starting", map[string]interface{}{
		"version": version,
		"library": "introspection-client v2.6.4",
	})

	// Register custom components (service-specific!)
	client.Register("health", func() interface{} {
		return createHealthData(startTime)
	})

	// Add more custom components as needed...

	// Start introspection client
	if err := client.Start(); err != nil {
		return nil, err
	}

	globalRecentLogs.Info("Introspection client started", map[string]interface{}{
		"server":   config.Environment,
		"url":      introspectionURL,
		"protocol": "HTTP/2 (mTLS 1.3)",
	})

	return &IntrospectionManager{
		client: client,
	}, nil
}

// createHealthData creates health status data
func createHealthData(startTime time.Time) interface{} {
	uptime := int(time.Since(startTime).Seconds())

	status := "healthy"
	if uptime < 10 {
		status = "starting"
	}

	return map[string]interface{}{
		"status":         status,
		"uptime_seconds": uptime,
	}
}

// Stop stops the introspection client gracefully
func (im *IntrospectionManager) Stop() {
	if im.client != nil {
		im.client.Stop()
	}
}
```

**Key points:**
- Replace `YOUR-SERVICE-NAME` with actual service name
- Adjust certificate paths for your service
- Add custom components as needed

---

## Step 4: Update `main.go`

Initialize introspection in your main function:

```go
func main() {
	// Track service start time
	startTime := time.Now()

	// Load configuration
	config := loadConfig()

	// Initialize introspection manager (ADR-032: Client Library)
	introspectionManager, err := NewIntrospectionManager(&config, startTime)
	if err != nil {
		log.Printf("⚠️  Failed to initialize introspection: %v (continuing without introspection)", err)
	}
	defer func() {
		if introspectionManager != nil {
			introspectionManager.Stop()
		}
	}()

	// ... rest of your service startup
}
```

---

## Step 5: Make PORT Required (NO DEFAULTS!)

Update your config loading:

**Before (BAD):**
```go
type Config struct {
	Port string  // string with default
}

func loadConfig() Config {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8100"  // ❌ BAD: hardcoded default
	}
	return Config{Port: port}
}
```

**After (GOOD):**
```go
type Config struct {
	Port int  // int, required!
}

func loadConfig() Config {
	// Parse PORT as int (required, no default)
	portStr := os.Getenv("PORT")
	if portStr == "" {
		log.Fatal("❌ FATAL: PORT environment variable is required")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		log.Fatalf("❌ FATAL: PORT must be a valid integer: %v", err)
	}

	return Config{
		Port: port,  // ✅ GOOD: required, type-safe
	}
}
```

**Update string formatting:**
```go
// Before
fmt.Sprintf(":%s", config.Port)  // string

// After
fmt.Sprintf(":%d", config.Port)  // int
```

---

## Step 6: Track HTTP Calls with ConnectivityTracker

For **every** HTTP call your service makes to another service, track it:

```go
// Example: HTTP client that tracks connectivity
type ServiceClient struct {
	httpClient          *http.Client
	connectivityTracker *standard.ConnectivityTracker
}

func NewServiceClient(tracker *standard.ConnectivityTracker) *ServiceClient {
	return &ServiceClient{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		connectivityTracker: tracker,
	}
}

func (c *ServiceClient) CallRemoteService(url string) error {
	// Track connectivity (start timer)
	startTime := time.Now()

	resp, err := c.httpClient.Get(url)
	latency := time.Since(startTime)

	if err != nil {
		// Track failed request
		if c.connectivityTracker != nil {
			c.connectivityTracker.TrackFailure("remote-service", url, latency, err.Error())
		}
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Track failed request (HTTP error)
		errorMsg := fmt.Sprintf("HTTP %d", resp.StatusCode)
		if c.connectivityTracker != nil {
			c.connectivityTracker.TrackFailure("remote-service", url, latency, errorMsg)
		}
		return fmt.Errorf("HTTP error: %d", resp.StatusCode)
	}

	// Track successful request
	if c.connectivityTracker != nil {
		c.connectivityTracker.TrackSuccess("remote-service", url, latency)
	}

	return nil
}
```

**Pass globalConnectivityTracker to your HTTP clients:**
```go
client := NewServiceClient(globalConnectivityTracker)
```

---

## Step 7: Use Structured Logging Everywhere

Replace all logging with structured logging:

**Before (BAD):**
```go
log.Printf("Deployment started for %s", appName)
log.Printf("Error: %v", err)
```

**After (GOOD):**
```go
LogInfo("Deployment started", map[string]interface{}{
	"app":   appName,
	"image": image,
})

LogError("Deployment failed", map[string]interface{}{
	"app":   appName,
	"error": err.Error(),
})
```

**ALWAYS include context!** Never log without a context map.

---

## Step 8: Version Bump

Since this is a significant change (introspection added):

**Minor version bump:**
- `1.0.0` → `1.1.0` (if adding introspection to existing service)

**Or major version bump if breaking changes:**
- `1.0.0` → `2.0.0` (if PORT is now required, etc.)

Update your `VERSION` file accordingly.

---

## Step 9: Testing

**1. Build check:**
```bash
go build
# Should compile without errors
```

**2. Environment check:**
```bash
# ❌ Should fail (PORT not set)
./your-service

# ✅ Should work
PORT=8100 ENVIRONMENT=staging ./your-service
```

**3. Introspection check:**

Query introspection API after deployment:
```bash
curl https://introspection.staging.eu-platform.com/api/entities \
  --cert ~/.config/platform-certs/your-service-to-introspection.pem \
  --key ~/.config/platform-certs/your-service-to-introspection-key.pem
```

**Verify your service appears with:**
- ✅ `service-info` component (version, port, uptime)
- ✅ `recent-logs` component (with your logs!)
- ✅ `inter-service-connectivity` component (if you make HTTP calls)
- ✅ `certificates` component (auto-scanned from /certs)
- ✅ `heartbeat` component (automatic)
- ✅ `metadata` with `status: "alive"`

---

## Common Patterns

### Pattern 1: HTTP Client with Connectivity Tracking

```go
type MyHTTPClient struct {
	client      *http.Client
	connectivity *standard.ConnectivityTracker
}

func (c *MyHTTPClient) DoRequest(url string) error {
	start := time.Now()
	resp, err := c.client.Get(url)
	latency := time.Since(start)

	if err != nil {
		c.connectivity.TrackFailure("service-name", url, latency, err.Error())
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		c.connectivity.TrackFailure("service-name", url, latency, fmt.Sprintf("HTTP %d", resp.StatusCode))
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	c.connectivity.TrackSuccess("service-name", url, latency)
	return nil
}
```

### Pattern 2: Custom Component Registration

```go
// Register a custom component that returns dynamic data
client.Register("my-custom-component", func() interface{} {
	return map[string]interface{}{
		"field1": "value1",
		"field2": computeSomeValue(),
		"field3": getMetrics(),
	}
})
```

### Pattern 3: Structured Logging Helpers

```go
// Create service-specific logging helpers
func LogDeploymentStarted(appName, image string) {
	LogInfo("Deployment started", map[string]interface{}{
		"app":   appName,
		"image": image,
	})
}

func LogDeploymentFailed(appName string, err error) {
	LogError("Deployment failed", map[string]interface{}{
		"app":   appName,
		"error": err.Error(),
	})
}
```

---

## Library Features You Get Automatically

### Auto-Registered Standard Components

The library automatically registers these components:

1. **service-info** - Service name, version, port, uptime, PID
2. **recent-logs** - Last 100 log entries (ringbuffer)
3. **inter-service-connectivity** - HTTP call tracking (latency, success rate, errors)
4. **certificates** - All certificates in CertDir with expiry dates
5. **heartbeat** - Liveness signal (59s interval, idle_since tracking)

### Ghost Detection

The library automatically tracks:
- **Entity ghosts** - Service hasn't sent heartbeat in >5 minutes
- **Component ghosts** - Component no longer in checksum exchange

All entities and components have metadata:
```json
{
  "metadata": {
    "status": "alive",
    "last_seen_at": "2025-11-12T20:00:00Z",
    "created_at": "2025-11-12T18:00:00Z",
    "became_ghost_at": null
  }
}
```

### Logging Features

- ✅ Logs go to BOTH introspection AND stdout/journald
- ✅ Error/Warn trigger immediate sync (critical!)
- ✅ Ringbuffer keeps last 100 entries
- ✅ Stats tracked (error_count, warn_count, etc.)

### Connectivity Features

- ✅ Tracks last hour of calls per service
- ✅ Calculates success rate, latency percentiles (p50/p95/p99)
- ✅ Keeps recent errors (last 5)
- ✅ Status: healthy (>95%), degraded (>90%), unhealthy (<90%)

---

## Troubleshooting

### "PORT environment variable is required"

✅ **Solution:** Set PORT explicitly (no defaults!)
```bash
PORT=8100 ./your-service
```

### "failed to load client certificate"

✅ **Solution:** Check certificate paths in introspection.go
```go
certPath := "/certs/your-service-to-introspection.cert.pem"
```

### Service not visible in introspection

✅ **Solution:** Check logs for introspection errors:
```bash
journalctl -u your-service | grep introspection
```

Common causes:
- Wrong INTROSPECTION_URL
- Missing certificates
- Network connectivity issues

### Docker build fails with "git not found"

✅ **Solution:** Make sure you ran `go mod tidy` to add checksums to go.sum.

The Dockerfile should work without git if go.sum has correct checksums:
```dockerfile
FROM golang:1.25-alpine AS builder
WORKDIR /build
COPY go.mod go.sum* ./
RUN go mod download  # Works without git if go.sum has checksums!
```

---

## Real-World Examples

See these services for complete implementations:

**deployment-agent** (`services/deployment-agent/`)
- Complete library implementation
- HTTP call tracking (image-store)
- Structured logging throughout
- Custom components (docker-info, api-doc)

**app-manager** (`services/app-manager/`)
- Complete library implementation
- HTTP call tracking (deployment-agent)
- Helper logging functions
- Custom components (app-info)

**auth-gateway** (`services/auth-gateway/`)
- Complete library implementation
- HTTP call tracking (Kratos)
- Session validation logging

**system-agent** (`services/introspection/system-agent/`)
- Reference implementation
- System monitoring components

---

## Summary

**Integration in 5 steps:**
1. Add library dependency (`go get v2.6.4`)
2. Create `logging.go` (global instances)
3. Create `introspection.go` (manager setup)
4. Update `main.go` (initialize)
5. Track HTTP calls + use structured logging

**Benefits:**
- ✅ Automatic introspection visibility
- ✅ Ghost detection (alive vs historical)
- ✅ Structured logging tracked
- ✅ HTTP call latency/success tracking
- ✅ Zero protocol complexity

**Happy integrating!** 🎉

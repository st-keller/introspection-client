// Package standard provides standard component implementations.
package standard

import (
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"time"

)

// ServiceType represents how the service is running
type ServiceType string

const (
	ServiceTypeSystemd    ServiceType = "systemd"
	ServiceTypeDocker     ServiceType = "docker"
	ServiceTypeStandalone ServiceType = "standalone"
)

// ServiceInfo holds service runtime information.
// Complete spec: name, version, pid, port, start_time (UTC timestamp),
// type, implementation_language, binary_path, working_directory, user, uid, gid
type ServiceInfo struct {
	ServiceName              string
	Version                  string
	Port                     int
	StartTime                time.Time
	ServiceType              ServiceType
	ImplementationLanguage   string
	BinaryPath               string
	WorkingDirectory         string
	User                     string
	UID                      int
	GID                      int
}

// AutoDetect creates ServiceInfo with auto-detected runtime information.
func AutoDetect(serviceName, version string, port int) *ServiceInfo {
	// Capture actual UTC timestamp at creation (STATIC!)
	startTime := time.Now().UTC()

	// Detect service type
	serviceType := detectServiceType()

	// Get binary path (current executable)
	binaryPath, _ := os.Executable()
	if binaryPath != "" {
		// Resolve symlinks
		if resolved, err := filepath.EvalSymlinks(binaryPath); err == nil {
			binaryPath = resolved
		}
	}

	// Get working directory
	workingDir, _ := os.Getwd()

	// Get user information
	userName := "unknown"
	uid := 0
	gid := 0

	if currentUser, err := user.Current(); err == nil {
		userName = currentUser.Username
		if parsedUID, err := strconv.Atoi(currentUser.Uid); err == nil {
			uid = parsedUID
		}
		if parsedGID, err := strconv.Atoi(currentUser.Gid); err == nil {
			gid = parsedGID
		}
	}

	return &ServiceInfo{
		ServiceName:            serviceName,
		Version:                version,
		Port:                   port,
		StartTime:              startTime,
		ServiceType:            serviceType,
		ImplementationLanguage: "go",
		BinaryPath:             binaryPath,
		WorkingDirectory:       workingDir,
		User:                   userName,
		UID:                    uid,
		GID:                    gid,
	}
}

// GetData converts ServiceInfo to component data (data-driven!).
func (s *ServiceInfo) GetData() interface{} {
	// Format start_time as RFC3339 without nanoseconds, using +00:00 (not Z)
	// This ensures consistency with Rust services and proper enrichment by mcp-sidecar
	startTimeFormatted := s.StartTime.Format("2006-01-02T15:04:05+00:00")

	data := map[string]interface{}{
		"name":                    s.ServiceName,
		"version":                 s.Version,
		"pid":                     os.Getpid(),
		"port":                    s.Port,
		"start_time":              startTimeFormatted,
		"type":                    string(s.ServiceType),
		"implementation_language": s.ImplementationLanguage,
		"binary_path":             s.BinaryPath,
		"working_directory":       s.WorkingDirectory,
		"user":                    s.User,
		"uid":                     s.UID,
		"gid":                     s.GID,
	}

	return data
}

// detectServiceType determines how the service is running.
func detectServiceType() ServiceType {
	// Check for systemd (INVOCATION_ID environment variable)
	if os.Getenv("INVOCATION_ID") != "" {
		return ServiceTypeSystemd
	}

	// Check for Docker (/.dockerenv file or cgroup)
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return ServiceTypeDocker
	}

	// Check cgroup for docker/containerd
	if data, err := os.ReadFile("/proc/self/cgroup"); err == nil {
		cgroup := string(data)
		if containsAny(cgroup, []string{"docker", "containerd"}) {
			return ServiceTypeDocker
		}
	}

	// Check if PID 1 is systemd
	if data, err := os.ReadFile("/proc/1/comm"); err == nil {
		if string(data) == "systemd\n" {
			return ServiceTypeSystemd
		}
	}

	return ServiceTypeStandalone
}

// containsAny checks if string contains any of the substrings.
func containsAny(s string, substrs []string) bool {
	for _, substr := range substrs {
		if len(s) >= len(substr) {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
		}
	}
	return false
}

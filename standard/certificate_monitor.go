package standard

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

)

// CertificateMonitor tracks X.509 certificates for expiry and metadata
type CertificateMonitor struct {
	certDir   string
	mu        sync.RWMutex
	certs     map[string]*CertificateInfo
	lastScan  time.Time
	scanError error
}

// CertificateInfo holds parsed certificate metadata
type CertificateInfo struct {
	Path            string    `json:"path"`
	Purpose         string    `json:"purpose"`           // "server", "client", "ca", "ca-chain"
	Subject         string    `json:"subject"`
	Issuer          string    `json:"issuer"`
	ValidFrom       time.Time `json:"valid_from"`
	ValidUntil      time.Time `json:"valid_until"`
	DaysUntilExpiry int       `json:"days_until_expiry"`
	SANs            []string  `json:"sans"`
	IsExpired       bool      `json:"is_expired"`
	ExpiryWarning   bool      `json:"expiry_warning"` // true if < 30 days
}

// NewCertificateMonitor creates a new certificate monitor for the given directory
func NewCertificateMonitor(certDir string) *CertificateMonitor {
	return &CertificateMonitor{
		certDir: certDir,
		certs:   make(map[string]*CertificateInfo),
	}
}

// Scan discovers and parses all *.cert.pem files in the certificate directory
func (cm *CertificateMonitor) Scan() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.certs = make(map[string]*CertificateInfo)
	cm.lastScan = time.Now()

	// Find all *.cert.pem files
	pattern := filepath.Join(cm.certDir, "*.cert.pem")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		cm.scanError = fmt.Errorf("failed to scan certificate directory: %w", err)
		return cm.scanError
	}

	// Parse each certificate
	for _, path := range matches {
		certInfo, err := parseCertificateFile(path)
		if err != nil {
			// Log warning but continue with other certs
			cm.scanError = fmt.Errorf("failed to parse %s: %w", path, err)
			continue
		}

		// Determine purpose from filename
		filename := filepath.Base(path)
		certInfo.Purpose = determinePurpose(filename)

		cm.certs[filename] = certInfo
	}

	cm.scanError = nil
	return nil
}

// ToComponent converts the certificate monitor state to an introspection component
func (cm *CertificateMonitor) GetData() interface{} {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	// Convert map to component data
	certData := make(map[string]interface{})
	for filename, info := range cm.certs {
		certData[filename] = map[string]interface{}{
			"path":              info.Path,
			"purpose":           info.Purpose,
			"subject":           info.Subject,
			"issuer":            info.Issuer,
			"valid_from":        info.ValidFrom.Format(time.RFC3339),
			"valid_until":       info.ValidUntil.Format(time.RFC3339),
			"days_until_expiry": info.DaysUntilExpiry,
			"sans":              info.SANs,
			"is_expired":        info.IsExpired,
			"expiry_warning":    info.ExpiryWarning,
		}
	}

	return certData
}

// GetExpiringCertificates returns certificates expiring within the given number of days
func (cm *CertificateMonitor) GetExpiringCertificates(withinDays int) []*CertificateInfo {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	var expiring []*CertificateInfo
	for _, cert := range cm.certs {
		if cert.DaysUntilExpiry <= withinDays && !cert.IsExpired {
			expiring = append(expiring, cert)
		}
	}
	return expiring
}

// GetExpiredCertificates returns all expired certificates
func (cm *CertificateMonitor) GetExpiredCertificates() []*CertificateInfo {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	var expired []*CertificateInfo
	for _, cert := range cm.certs {
		if cert.IsExpired {
			expired = append(expired, cert)
		}
	}
	return expired
}

// parseCertificateFile reads and parses a PEM-encoded certificate file
func parseCertificateFile(path string) (*CertificateInfo, error) {
	// Read certificate file
	certPEM, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read certificate: %w", err)
	}

	// Decode first PEM block (for ca-chain, this will be the first cert)
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block")
	}

	// Parse certificate
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse certificate: %w", err)
	}

	// Calculate expiry information
	now := time.Now()
	daysUntilExpiry := int(time.Until(cert.NotAfter).Hours() / 24)
	isExpired := now.After(cert.NotAfter)
	expiryWarning := daysUntilExpiry <= 30 && !isExpired

	// Collect SANs (Subject Alternative Names)
	var sans []string
	for _, dns := range cert.DNSNames {
		sans = append(sans, fmt.Sprintf("DNS:%s", dns))
	}
	for _, ip := range cert.IPAddresses {
		sans = append(sans, fmt.Sprintf("IP:%s", ip.String()))
	}

	return &CertificateInfo{
		Path:            path,
		Subject:         cert.Subject.String(),
		Issuer:          cert.Issuer.String(),
		ValidFrom:       cert.NotBefore,
		ValidUntil:      cert.NotAfter,
		DaysUntilExpiry: daysUntilExpiry,
		SANs:            sans,
		IsExpired:       isExpired,
		ExpiryWarning:   expiryWarning,
	}, nil
}

// determinePurpose infers certificate purpose from filename
func determinePurpose(filename string) string {
	lower := strings.ToLower(filename)

	// CA certificates
	if strings.Contains(lower, "ca-chain") {
		return "ca-chain"
	}
	if strings.Contains(lower, "ca.cert") || lower == "ca.cert.pem" {
		return "ca"
	}

	// Client certificates (to other services)
	if strings.Contains(lower, "-to-") {
		return "client"
	}

	// Server certificates (this service)
	return "server"
}

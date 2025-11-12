package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	introspection "github.com/st-keller/introspection-client/v2"
)

func main() {
	log.Println("🚀 Starting example service with introspection-client v2.5")

	// Initialize introspection client (all standard components auto-registered!)
	client, err := introspection.New(introspection.Config{
		ServiceName:      "example-service",
		Version:          "1.0.0",
		Port:             8080,
		Server:           "staging",
		IntrospectionURL: "https://introspection:9080",
		CertPath:         "/certs/example-service-to-introspection.cert.pem",
		KeyPath:          "/certs/example-service-to-introspection.key.pem",
		CAPath:           "/certs/ca.cert.pem",
		CertDir:          "/certs",
	})
	if err != nil {
		log.Fatalf("❌ Failed to create introspection client: %v", err)
	}

	// Register custom component (optional - standard components already registered!)
	client.Register("health", func() interface{} {
		return map[string]interface{}{
			"status": "healthy",
			"checks": []map[string]interface{}{
				{"name": "database", "ok": true},
				{"name": "cache", "ok": true},
			},
		}
	}, nil) // OnlyTrigger (no periodic updates)

	// Start background systems (Heartbeat, Update, Sync)
	if err := client.Start(); err != nil {
		log.Fatalf("❌ Failed to start introspection client: %v", err)
	}
	defer client.Stop()

	log.Println("✅ Introspection client running!")
	log.Println("   📦 Standard components: service-info, recent-logs, connectivity, certificates")
	log.Println("   ⏱️  Heartbeat: 59s")
	log.Println("")

	// Example 1: Logging (goes to introspection!)
	log.Println("📝 Example 1: Logging")
	client.GetLogs().Info("Service started successfully", map[string]interface{}{
		"version": "1.0.0",
		"port":    8080,
	})

	client.GetLogs().Warn("This is a warning", map[string]interface{}{
		"reason": "demonstration",
	})

	// Example 2: Track connectivity (goes to introspection!)
	log.Println("🔗 Example 2: Connectivity tracking")
	start := time.Now()
	// Simulate HTTP call
	time.Sleep(50 * time.Millisecond)
	latency := time.Since(start)
	client.GetConnectivity().TrackSuccess("introspection", "https://introspection:9080", latency)

	// Example 3: Trigger custom component update
	log.Println("🔄 Example 3: Trigger component update")
	if err := client.TriggerUpdate("health"); err != nil {
		log.Printf("⚠️  Failed to trigger update: %v", err)
	}

	// Example 4: Check certificate expiry
	log.Println("🔐 Example 4: Certificate monitoring")
	expiring := client.GetCertMonitor().GetExpiringCertificates(30) // Within 30 days
	if len(expiring) > 0 {
		log.Printf("⚠️  %d certificates expiring soon!", len(expiring))
	} else {
		log.Println("✅ No certificates expiring in next 30 days")
	}

	log.Println("")
	log.Println("🎯 All examples done! Service running...")
	log.Println("   Press Ctrl+C to stop")

	// Wait for interrupt
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("🛑 Shutting down...")
}

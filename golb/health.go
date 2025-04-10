package golb

import (
	"context"
	"log"
	"net/http"
	"time"
)

// performHealthCheckCycle runs one round of health checks for all backends
func (s *ServerPool) performHealthCheckCycle(client *http.Client, cfg *Config) {
	log.Println("Performing health checks...")
	for _, b := range s.backends {
		// Perform check and get duration
		alive, duration := isBackendAlive(client, b, cfg.HealthCheckPath)

		// Update status if changed and log
		currentStatus := b.IsAlive()
		if currentStatus != alive {
			statusStr := "DOWN"
			if alive {
				statusStr = "UP"
			}
			log.Printf("HealthCheck: Backend %s status changed to [%s]", b.URL, statusStr)
			b.SetAlive(alive)
		}

		// Update response time metric if the check was successful
		if alive && duration > 0 {
			s.lb.UpdateResponseTime(b, duration) // Update EWMA etc. via interface
		}
	}
}

// isBackendAlive performs a single health check GET request
// Returns alive status and the duration of the check.
func isBackendAlive(client *http.Client, b *Backend, healthCheckPath string) (bool, time.Duration) {
	healthURL := b.URL.String() + healthCheckPath
	startTime := time.Now()

	req, err := http.NewRequestWithContext(context.Background(), "GET", healthURL, nil)
	if err != nil {
		// Log locally, don't affect overall check status necessarily here
		log.Printf("Error creating health check request for %s: %v", b.URL, err)
		return false, 0 // Cannot reach, definitely not alive
	}

	resp, err := client.Do(req)
	duration := time.Since(startTime) // Measure duration regardless of success/failure

	if err != nil {
		// Network errors mean it's down
		// log.Printf("Health check failed for %s: %v\n", b.URL, err) // Can be noisy
		return false, duration
	}
	defer func() {
		cerr := resp.Body.Close()
		if cerr != nil && err != nil {
			log.Printf("Error closing response body for %s: %v", b.URL, err)
			err = cerr
		}
	}()

	// Any status other than 200 OK means unhealthy
	if resp.StatusCode != http.StatusOK {
		// log.Printf("Health check non-OK for %s: Status %d\n", b.URL, resp.StatusCode) // Can be noisy
		return false, duration
	}

	// Success!
	return true, duration
}

package golb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
)

// BackendStatus holds information for the /status endpoint response for one backend
type BackendStatus struct {
	URL               string      `json:"url"`
	Alive             bool        `json:"alive"`
	Weight            int         `json:"weight,omitempty"` // Include weight if configured
	ActiveConnections int64       `json:"activeConnections,omitempty"`
	EWMANanoSec       int64       `json:"ewmaNanoSec,omitempty"`
	Info              interface{} `json:"info,omitempty"` // Use interface{} for arbitrary JSON
	InfoError         string      `json:"infoError,omitempty"`
}

// StatusHandler provides the status of all configured backends
func StatusHandler(w http.ResponseWriter, r *http.Request, pool *ServerPool, cfg *Config) {
	statuses := make([]BackendStatus, 0, len(pool.backends))
	client := &http.Client{
		Timeout: cfg.BackendRequestTimeout, // Use configured timeout
	}

	var wg sync.WaitGroup
	var mu sync.Mutex // Protects the statuses slice append

	for _, b := range pool.backends {
		wg.Add(1)
		// Fetch info concurrently for each backend
		go func(backend *Backend) {
			defer wg.Done()

			// Basic status from pool state
			status := BackendStatus{
				URL:   backend.URL.String(),
				Alive: backend.IsAlive(),
				// Include LB-specific state if desired
				Weight:            backend.GetWeight(),
				ActiveConnections: backend.activeConnections.Load(),
				EWMANanoSec:       backend.ewmaResponseTime.Load(),
			}

			// Fetch /info endpoint data
			infoURL := backend.URL.String() + cfg.InfoPath // Use configured path
			req, err := http.NewRequestWithContext(context.Background(), "GET", infoURL, nil)
			if err != nil {
				status.InfoError = fmt.Sprintf("failed to create info request: %v", err)
				// Safely append status even with error
				mu.Lock()
				statuses = append(statuses, status)
				mu.Unlock()
				return
			}

			resp, err := client.Do(req)
			if err != nil {
				status.InfoError = fmt.Sprintf("info request failed: %v", err)
				mu.Lock()
				statuses = append(statuses, status)
				mu.Unlock()
				return
			}
			defer func() {
				cerr := resp.Body.Close()
				if cerr != nil && err != nil {
					log.Printf("Error closing response body for %s: %v", b.URL, err)
					err = cerr
				}
			}()

			bodyBytes, err := io.ReadAll(resp.Body)
			if err != nil {
				status.InfoError = fmt.Sprintf("failed to read info body: %v", err)
				mu.Lock()
				statuses = append(statuses, status)
				mu.Unlock()
				return
			}

			if resp.StatusCode != http.StatusOK {
				status.InfoError = fmt.Sprintf("info endpoint returned status %d, body: %s", resp.StatusCode, string(bodyBytes))
			} else {
				// Attempt to unmarshal as JSON, otherwise store as string
				var infoData interface{}
				err = json.Unmarshal(bodyBytes, &infoData)
				if err != nil {
					status.Info = string(bodyBytes) // Store raw string if not JSON
					status.InfoError = fmt.Sprintf("info body is not valid JSON: %v", err)
				} else {
					status.Info = infoData
				}
			}

			// Append final status for this backend
			mu.Lock()
			statuses = append(statuses, status)
			mu.Unlock()

		}(b)
	}

	wg.Wait() // Wait for all info fetches to complete

	// Respond with collected statuses
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(statuses); err != nil {
		log.Printf("Error encoding status response: %v", err)
		http.Error(w, `{"error": "Failed to generate status"}`, http.StatusInternalServerError)
	}
}

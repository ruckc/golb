package golb

import (
	"net/http"
	"net/url"
	"time"
)

// ServerPool holds the collection of backends and the load balancing strategy
type ServerPool struct {
	backends []*Backend
	lb       LoadBalancer
}

// NewServerPool creates a new ServerPool with a specific load balancing strategy
func NewServerPool(lbStrategy LoadBalancer) *ServerPool {
	return &ServerPool{
		backends: []*Backend{},
		lb:       lbStrategy,
	}
}

// AddBackend adds a new backend server to the pool
func (s *ServerPool) AddBackend(b *Backend) {
	s.backends = append(s.backends, b)
}

// GetNextPeer selects the next available backend using the configured strategy
func (s *ServerPool) GetNextPeer() *Backend {
	// Delegate selection to the load balancer strategy
	return s.lb.SelectBackend(s.backends)
}

// MarkBackendStatus updates the Alive status of a specific backend by URL
func (s *ServerPool) MarkBackendStatus(backendURL *url.URL, alive bool) {
	if backendURL == nil {
		return
	}
	targetURLStr := backendURL.String()
	for _, b := range s.backends {
		if b.URL.String() == targetURLStr {
			b.SetAlive(alive)
			return
		}
	}
}

// HealthCheck starts the periodic health checking process for all backends
func (s *ServerPool) HealthCheck(cfg *Config) {
	// Use a single client for all health checks in this cycle for efficiency
	client := &http.Client{
		Timeout: cfg.BackendRequestTimeout,
		// Consider customizing transport if needed (e.g., disable keep-alives)
		// Transport: &http.Transport{ DisableKeepAlives: true },
	}

	// Perform initial check immediately
	s.performHealthCheckCycle(client, cfg)

	// Start ticker for subsequent checks
	ticker := time.NewTicker(cfg.HealthCheckInterval)
	defer ticker.Stop()

	for range ticker.C {
		s.performHealthCheckCycle(client, cfg)
	}
}

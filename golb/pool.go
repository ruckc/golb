package golb

import (
	"context"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// ServerPool holds the collection of backends and the load balancing strategy
type ServerPool struct {
	backends []*Backend
	lb       LoadBalancer

	mu               sync.Mutex
	backendAvailable *sync.Cond
}

// NewServerPool creates a new ServerPool with a specific load balancing strategy
func NewServerPool(lbStrategy LoadBalancer) *ServerPool {
	pool := &ServerPool{
		backends: []*Backend{},
		lb:       lbStrategy,
	}
	pool.backendAvailable = sync.NewCond(&pool.mu)
	return pool
}

// AddBackend adds a new backend server to the pool
func (s *ServerPool) AddBackend(b *Backend) {
	s.backends = append(s.backends, b)
}

// GetNextPeer selects the next available backend using the configured strategy
// It blocks and waits for an available backend if none are currently alive.
// It returns nil if the context is canceled or times out.
func (s *ServerPool) GetNextPeer(ctx context.Context) *Backend {
	s.mu.Lock()
	defer s.mu.Unlock()

	for {
		backend := s.lb.SelectBackend(s.backends)
		if backend != nil {
			return backend
		}

		// Wait for a backend to become available or context cancellation
		// Removed unused waitCh variable

		// Wait for backendAvailable or context done
		waitDone := make(chan struct{})
		go func() {
			s.mu.Lock()
			s.backendAvailable.Wait()
			s.mu.Unlock()
			close(waitDone)
		}()

		s.mu.Unlock()
		select {
		case <-ctx.Done():
			s.mu.Lock()
			return nil
		case <-waitDone:
			s.mu.Lock()
		}
	}
}

// MarkBackendStatus updates the Alive status of a specific backend by URL
func (s *ServerPool) MarkBackendStatus(backendURL *url.URL, alive bool) {
	if backendURL == nil {
		return
	}
	targetURLStr := backendURL.String()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range s.backends {
		if b.URL.String() == targetURLStr {
			previousAlive := b.IsAlive()
			b.SetAlive(alive)
			if !previousAlive && alive {
				// Notify waiters that a backend became available
				s.backendAvailable.Broadcast()
			}
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

	// Start ticker for subsequent checks
	ticker := time.NewTicker(cfg.HealthCheckInterval)
	defer ticker.Stop()

	for range ticker.C {
		s.PerformHealthCheckCycle(client, cfg)
	}
}

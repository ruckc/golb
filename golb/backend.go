package golb

import (
	"net/http/httputil"
	"net/url"
	"sync"
	"sync/atomic"
)

// Backend holds information and state about a single backend server
type Backend struct {
	URL          *url.URL
	Alive        atomic.Bool // Tracks health status
	ReverseProxy *httputil.ReverseProxy

	// --- State for Load Balancing Strategies ---
	// Mutex protects state fields not handled atomically (e.g., currentWeight)
	stateMutex sync.Mutex
	// Least Connections: Count of active connections proxied *to* this backend
	activeConnections atomic.Int64
	// Least Response Time: EWMA of response times in nanoseconds
	ewmaResponseTime atomic.Int64
	// Weighted Round Robin: Static weight assigned at config time
	weight int
	// Weighted Round Robin: Internal algorithm state
	currentWeight int
}

// NewBackend creates a new Backend instance
func NewBackend(targetURL *url.URL, proxy *httputil.ReverseProxy, weight int) *Backend {
	b := &Backend{
		URL:          targetURL,
		ReverseProxy: proxy,
		weight:       weight, // Assign weight during creation
		// Atomics default to 0, Alive defaults to false (needs first health check)
	}
	b.Alive.Store(false) // Start as not alive
	return b
}

// SetAlive safely sets the alive status of the backend
func (b *Backend) SetAlive(alive bool) {
	b.Alive.Store(alive)
}

// IsAlive safely checks the alive status of the backend
func (b *Backend) IsAlive() bool {
	return b.Alive.Load()
}

// IncrementActiveConnections atomically increases the connection count
// NOTE: Call this when a request is successfully routed TO this backend.
func (b *Backend) IncrementActiveConnections() {
	b.activeConnections.Add(1)
}

// DecrementActiveConnections atomically decreases the connection count
// NOTE: Call this when a request routed TO this backend finishes or errors.
func (b *Backend) DecrementActiveConnections() {
	b.activeConnections.Add(-1)
}

// GetWeight returns the static weight of the backend
func (b *Backend) GetWeight() int {
	return b.weight
}

// Note: Get/Set for ewmaResponseTime and activeConnections are handled via atomics directly
// or through the LoadBalancer interface methods where applicable (e.g., UpdateResponseTime)

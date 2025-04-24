package golb

import (
	"log"
	"net/http"
)

// Lb is the main request handler, selecting a backend and proxying the request
func Lb(w http.ResponseWriter, r *http.Request, pool *ServerPool) {
	// --- Connection Tracking Start (Conceptual) ---
	// For LeastConnections, this is where you might potentially increment
	// the connection count *after* successfully selecting a peer.
	// However, doing it accurately before knowing if the proxy succeeds is hard.
	// Accurate tracking often requires wrapping http.ResponseWriter or Transport.
	// var selectedPeer *Backend // Keep track if needed for decrement later

	peer := pool.GetNextPeer(r.Context())
	if peer == nil {
		log.Printf("Service Unavailable: No healthy backends available for request %s %s", r.Method, r.URL.Path)
		http.Error(w, "Service unavailable", http.StatusServiceUnavailable)
		return
	}

	// If using LeastConnections, potentially increment here:
	// peer.IncrementActiveConnections()
	// selectedPeer = peer // Store for potential decrement later

	// Defer decrement if using simple approach (less accurate)
	// defer func() {
	//     if selectedPeer != nil {
	//         selectedPeer.DecrementActiveConnections()
	//     }
	// }()

	log.Printf("Forwarding %s %s to backend %s", r.Method, r.URL.Path, peer.URL)
	// Delegate to the ReverseProxy instance associated with the chosen backend
	// The ReverseProxy's ErrorHandler (configured in main) will handle connection errors
	peer.ReverseProxy.ServeHTTP(w, r)

	// --- Connection Tracking End (Conceptual) ---
	// If not using defer, decrement would happen here for successful requests.
	// Error handler needs to handle decrement for failed proxy attempts.
}

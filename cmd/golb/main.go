package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ruckc/golb/golb" // Import your library package
)

func main() {
	// --- Configuration Loading ---
	cfg, err := golb.LoadConfig()
	if err != nil {
		log.Fatalf("Configuration error: %v", err)
	}

	// --- Load Balancer Strategy Selection ---
	var lb golb.LoadBalancer
	switch cfg.LoadBalancingAlgorithm {
	case "least-connections":
		lb = golb.NewLeastConnectionBalancer()
		log.Println("Using Load Balancer: Least Connections")
		log.Println("NOTE: Connection counting increment/decrement logic needs external implementation (handler/transport wrapping).")
	case "least-response-time":
		lb = golb.NewLeastResponseTimeBalancer(cfg.EWMAAlpha) // Pass alpha from config
		log.Printf("Using Load Balancer: Least Response Time (EWMA Alpha: %.2f)", cfg.EWMAAlpha)
		log.Println("NOTE: Response times updated via health check durations.")
	case "weighted-round-robin":
		lb = golb.NewWeightedRoundRobinBalancer()
		log.Println("Using Load Balancer: Weighted Round Robin")
		if len(cfg.BackendWeights) != len(cfg.BackendServers) {
			log.Printf("Warning: Weights ignored due to count mismatch. Falling back to equal weights implicitly (like Round Robin).")
			// Optionally, create a RoundRobinBalancer instead if weights are invalid
			// lb = golb.NewRoundRobinBalancer()
		}
	case "round-robin":
		fallthrough // Explicit fallthrough
	default:
		if cfg.LoadBalancingAlgorithm != "round-robin" {
			log.Printf("Warning: Unknown load balancing algorithm '%s', defaulting to round-robin.", cfg.LoadBalancingAlgorithm)
		}
		lb = golb.NewRoundRobinBalancer()
		log.Println("Using Load Balancer: Round Robin")
		cfg.LoadBalancingAlgorithm = "round-robin" // Ensure config reflects the actual used algo
	}

	// --- Server Pool Initialization ---
	pool := golb.NewServerPool(lb)

	// --- Backend Initialization ---
	for i, backendAddr := range cfg.BackendServers {
		backendURL, err := url.Parse(backendAddr)
		if err != nil {
			log.Printf("Warning: Failed to parse backend URL '%s': %v. Skipping.", backendAddr, err)
			continue
		}

		// Create the reverse proxy instance for this backend
		proxy := httputil.NewSingleHostReverseProxy(backendURL)

		// Customize Director
		defaultDirector := proxy.Director
		proxy.Director = func(req *http.Request) {
			defaultDirector(req)
			req.Host = backendURL.Host // Important for virtual hosting
		}

		// Customize Error Handler - needs access to pool to mark status
		proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("Proxy error forwarding to %s: %v", backendURL, err)
			pool.MarkBackendStatus(backendURL, false) // Mark down on proxy errors

			// --- Connection Tracking Decrement (Conceptual for LC) ---
			// If using LeastConnections, decrement counter on error
			// Find backend 'b' corresponding to backendURL and call:
			// b.DecrementActiveConnections()

			// Provide appropriate HTTP error
			if errors.Is(err, context.Canceled) || errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) {
				// Client disconnected or connection reset
				http.Error(w, "Client Closed Request", 499) // Nginx's code
			} else {
				// Other errors (connection refused, timeout during proxying)
				http.Error(w, "Bad Gateway", http.StatusBadGateway)
			}
		}

		// Determine weight for WRR
		weight := 1 // Default weight if not specified or counts mismatch
		if cfg.LoadBalancingAlgorithm == "weighted-round-robin" && len(cfg.BackendWeights) == len(cfg.BackendServers) && i < len(cfg.BackendWeights) {
			weight = cfg.BackendWeights[i]
			if weight < 0 {
				log.Printf("Warning: Backend %s has negative weight (%d), treating as 0.", backendAddr, weight)
				weight = 0
			}
		}

		// Create and add the backend to the pool
		backendInstance := golb.NewBackend(backendURL, proxy, weight)
		pool.AddBackend(backendInstance)
		log.Printf("Configured backend: %s (Weight: %d)", backendAddr, weight)
	}

	// --- Initial Health Check (Synchronous) ---
	log.Println("Performing initial health check...")
	// Create a client specifically for this initial check
	initialCheckClient := &http.Client{
		Timeout: cfg.BackendRequestTimeout, // Use configured timeout
	}
	// Call performHealthCheckCycle directly (it's defined in health.go but accessible via pool)
	pool.PerformHealthCheckCycle(initialCheckClient, cfg) // You need to expose performHealthCheckCycle or call it via HealthCheck differently
	log.Println("Initial health check complete.")

	// Ensure at least one valid backend was added
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if pool.GetNextPeer(ctx) == nil && len(cfg.BackendServers) > 0 {
		log.Fatal("Error: No valid backend servers were successfully configured.")
	} else if len(cfg.BackendServers) == 0 {
		log.Fatal("Error: No backend servers defined in configuration.") // Should be caught by LoadConfig, but double check
	}

	// --- Start Background Tasks ---
	go pool.HealthCheck(cfg)

	// --- HTTP Server Setup ---
	mux := http.NewServeMux()

	// Status endpoint handler (closure captures pool and cfg)
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		golb.StatusHandler(w, r, pool, cfg)
	})

	// Main proxy handler (closure captures pool)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// --- Connection Tracking Increment/Decrement (Conceptual) ---
		// This is where you would wrap the handler or ResponseWriter
		// to accurately track connection start/end for LeastConnections.
		// E.g., peer := pool.GetNextPeer(); if peer != nil { peer.Increment... }
		//       defer peer.Decrement...
		golb.Lb(w, r, pool, cfg.AccessLogEnabled, cfg.AccessLogPayloads)
	})

	// Configure the server
	server := &http.Server{
		Addr:    cfg.ProxyPort,
		Handler: mux,
		// Add timeouts for production use (ReadTimeout, WriteTimeout, IdleTimeout)
		// ReadTimeout:  5 * time.Second,
		// WriteTimeout: 10 * time.Second,
		// IdleTimeout:  120 * time.Second,
	}

	// --- Start Server & Handle Shutdown ---
	go func() {
		log.Printf("Go Load Balancer (GoLB) started on port %s", cfg.ProxyPort)
		log.Printf("Using load balancing algorithm: %s", cfg.LoadBalancingAlgorithm)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("Could not listen on %s: %v\n", cfg.ProxyPort, err)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down server...")

	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second) // Allow 30 seconds for graceful shutdown
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("Server exiting")
}

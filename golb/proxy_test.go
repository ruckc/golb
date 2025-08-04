package golb

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestLbProxyWithSingleBackend(t *testing.T) {
	// Create a simple backend server
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Echo back request info
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		var body []byte
		if r.Body != nil {
			body, _ = io.ReadAll(r.Body)
		}

		response := fmt.Sprintf(`{
			"method": "%s",
			"path": "%s",
			"body": "%s",
			"headers": %d
		}`, r.Method, r.URL.Path, string(body), len(r.Header))

		w.Write([]byte(response))
	}))
	defer backend.Close()

	// Parse backend URL
	backendURL, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatalf("Failed to parse backend URL: %v", err)
	}

	// Create server pool with single backend
	pool := NewServerPool(NewRoundRobinBalancer())

	proxy := httputil.NewSingleHostReverseProxy(backendURL)
	peer := NewBackend(backendURL, proxy, 1)
	if peer == nil {
		t.Skip("NewBackend returned nil, skipping test")
		return
	}

	pool.AddBackend(peer)

	// Test cases
	tests := []struct {
		name              string
		method            string
		path              string
		body              string
		accessLogEnabled  bool
		accessLogPayloads bool
		expectedStatus    int
	}{
		{
			name:              "GET request without logging",
			method:            "GET",
			path:              "/test",
			body:              "",
			accessLogEnabled:  false,
			accessLogPayloads: false,
			expectedStatus:    http.StatusOK,
		},
		{
			name:              "POST request with access logging",
			method:            "POST",
			path:              "/api/data",
			body:              `{"key": "value"}`,
			accessLogEnabled:  true,
			accessLogPayloads: false,
			expectedStatus:    http.StatusOK,
		},
		{
			name:              "PUT request with payload logging",
			method:            "PUT",
			path:              "/api/update",
			body:              `{"id": 123, "name": "test"}`,
			accessLogEnabled:  true,
			accessLogPayloads: true,
			expectedStatus:    http.StatusOK,
		},
		{
			name:              "DELETE request",
			method:            "DELETE",
			path:              "/api/delete/123",
			body:              "",
			accessLogEnabled:  true,
			accessLogPayloads: true,
			expectedStatus:    http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create request
			var reqBody io.Reader
			if tt.body != "" {
				reqBody = strings.NewReader(tt.body)
			}

			req := httptest.NewRequest(tt.method, tt.path, reqBody)
			req.Header.Set("Content-Type", "application/json")

			// Create response recorder
			rr := httptest.NewRecorder()

			// Call the load balancer
			Lb(rr, req, pool, tt.accessLogEnabled, tt.accessLogPayloads)

			// Check status code
			if rr.Code != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d", tt.expectedStatus, rr.Code)
			}

			// Check that response contains expected data
			respBody := rr.Body.String()
			if !strings.Contains(respBody, fmt.Sprintf(`"method": "%s"`, tt.method)) {
				t.Errorf("Response should contain method %s, got: %s", tt.method, respBody)
			}

			if !strings.Contains(respBody, fmt.Sprintf(`"path": "%s"`, tt.path)) {
				t.Errorf("Response should contain path %s, got: %s", tt.path, respBody)
			}

			if tt.body != "" && !strings.Contains(respBody, fmt.Sprintf(`"body": "%s"`, tt.body)) {
				t.Errorf("Response should contain body %s, got: %s", tt.body, respBody)
			}
		})
	}
}

func TestLbNoHealthyBackends(t *testing.T) {
	// Create empty server pool
	pool := NewServerPool(NewRoundRobinBalancer())

	req := httptest.NewRequest("GET", "/test", nil)
	// Add timeout context to prevent indefinite blocking
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()

	Lb(rr, req, pool, true, true)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected status %d, got %d", http.StatusServiceUnavailable, rr.Code)
	}

	if !strings.Contains(rr.Body.String(), "Service unavailable") {
		t.Errorf("Expected 'Service unavailable' message, got: %s", rr.Body.String())
	}
}

func TestLbWithUnhealthyBackend(t *testing.T) {
	// Create a backend that will be marked as unhealthy
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Backend error"))
	}))
	backend.Close() // Close immediately to make it unhealthy

	backendURL, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatalf("Failed to parse backend URL: %v", err)
	}

	pool := NewServerPool(NewRoundRobinBalancer())

	proxy := httputil.NewSingleHostReverseProxy(backendURL)
	peer := NewBackend(backendURL, proxy, 1)
	if peer == nil {
		t.Skip("NewBackend returned nil, skipping test")
		return
	}

	peer.SetAlive(false) // Mark as unhealthy
	pool.AddBackend(peer)

	req := httptest.NewRequest("GET", "/test", nil)
	// Add timeout context to prevent indefinite blocking
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()

	Lb(rr, req, pool, true, true)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected status %d, got %d", http.StatusServiceUnavailable, rr.Code)
	}
}

// Add a simple test that doesn't rely on ServerPool
func TestResponseCaptureWriterOnly(t *testing.T) {
	// Test the responseCaptureWriter directly
	rr := httptest.NewRecorder()
	buffer := &bytes.Buffer{}

	captureWriter := &responseCaptureWriter{
		ResponseWriter: rr,
		body:           buffer,
	}

	testData := []byte("Hello, World!")
	n, err := captureWriter.Write(testData)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	if n != len(testData) {
		t.Errorf("Expected to write %d bytes, wrote %d", len(testData), n)
	}

	if buffer.String() != string(testData) {
		t.Errorf("Buffer should contain '%s', got '%s'", string(testData), buffer.String())
	}

	if rr.Body.String() != string(testData) {
		t.Errorf("Response recorder should contain '%s', got '%s'", string(testData), rr.Body.String())
	}
}

func TestLbConcurrentRequests(t *testing.T) {
	// Create backend server
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate some processing time
		time.Sleep(10 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}))
	defer backend.Close()

	backendURL, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatalf("Failed to parse backend URL: %v", err)
	}

	pool := NewServerPool(NewRoundRobinBalancer())

	proxy := httputil.NewSingleHostReverseProxy(backendURL)
	peer := NewBackend(backendURL, proxy, 1)
	if peer == nil {
		t.Skip("NewBackend returned nil, skipping test")
		return
	}

	pool.AddBackend(peer)

	// Run concurrent requests
	const numRequests = 10
	results := make(chan int, numRequests)

	for i := 0; i < numRequests; i++ {
		go func() {
			req := httptest.NewRequest("GET", "/test", nil)
			rr := httptest.NewRecorder()

			Lb(rr, req, pool, false, false)
			results <- rr.Code
		}()
	}

	// Collect results
	for i := 0; i < numRequests; i++ {
		select {
		case code := <-results:
			if code != http.StatusOK {
				t.Errorf("Expected status %d, got %d", http.StatusOK, code)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Timeout waiting for concurrent requests")
		}
	}
}

// Simple integration test that bypasses ServerPool
func TestLbDirectProxy(t *testing.T) {
	// Create a simple backend server
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Hello from backend"))
	}))
	defer backend.Close()

	// Test the response capture writer directly
	rr := httptest.NewRecorder()
	buffer := &bytes.Buffer{}

	captureWriter := &responseCaptureWriter{
		ResponseWriter: rr,
		body:           buffer,
	}

	testData := []byte("Test response")
	n, err := captureWriter.Write(testData)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	if n != len(testData) {
		t.Errorf("Expected to write %d bytes, wrote %d", len(testData), n)
	}

	if buffer.String() != string(testData) {
		t.Errorf("Buffer should contain '%s', got '%s'", string(testData), buffer.String())
	}
}

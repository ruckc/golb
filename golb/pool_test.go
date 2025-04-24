package golb

import (
	"context"
	"net/url"
	"sync"
	"testing"
	"time"
)

// mockLoadBalancer is a simple LoadBalancer for testing
type mockLoadBalancer struct {
	selectedBackend *Backend
}

func (m *mockLoadBalancer) SelectBackend(backends []*Backend) *Backend {
	return m.selectedBackend
}

func (m *mockLoadBalancer) UpdateResponseTime(backend *Backend, duration time.Duration) {}

// TestAddBackend verifies that backends are added correctly to the pool
func TestAddBackend(t *testing.T) {
	lb := &mockLoadBalancer{}
	pool := NewServerPool(lb)

	u, _ := url.Parse("http://localhost:8080")
	backend := NewBackend(u, nil, 1)
	pool.AddBackend(backend)

	if len(pool.backends) != 1 {
		t.Errorf("expected 1 backend, got %d", len(pool.backends))
	}
	if pool.backends[0] != backend {
		t.Errorf("backend in pool does not match added backend")
	}
}

// TestGetNextPeer returns the selected backend or blocks until available or context canceled
func TestGetNextPeer(t *testing.T) {
	lb := &mockLoadBalancer{}
	pool := NewServerPool(lb)

	u, _ := url.Parse("http://localhost:8080")
	backend := NewBackend(u, nil, 1)
	backend.SetAlive(true)

	// Set the mock to return the backend
	lb.selectedBackend = backend
	pool.AddBackend(backend)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	got := pool.GetNextPeer(ctx)
	if got != backend {
		t.Errorf("expected backend %v, got %v", backend, got)
	}

	// Test blocking behavior with no alive backend and context timeout
	lb.selectedBackend = nil
	ctx2, cancel2 := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel2()

	// Use a WaitGroup to ensure the goroutine finishes
	var wg sync.WaitGroup
	wg.Add(1)

	start := time.Now()
	var got2 *Backend
	go func() {
		defer wg.Done()
		got2 = pool.GetNextPeer(ctx2)
	}()
	// Wait a short time before signaling availability to avoid unlocking an unlocked mutex
	time.Sleep(10 * time.Millisecond)
	// Mark a backend alive to unblock GetNextPeer
	pool.MarkBackendStatus(u, true)

	wg.Wait()
	elapsed := time.Since(start)

	if got2 != nil {
		t.Errorf("expected nil backend due to timeout, got %v", got2)
	}
	if elapsed < 50*time.Millisecond {
		t.Errorf("expected blocking for at least 50ms, blocked for %v", elapsed)
	}
}

// TestMarkBackendStatus updates backend alive status and notifies waiters
func TestMarkBackendStatus(t *testing.T) {
	lb := &mockLoadBalancer{}
	pool := NewServerPool(lb)

	u, _ := url.Parse("http://localhost:8080")
	backend := NewBackend(u, nil, 1)
	backend.SetAlive(false)
	pool.AddBackend(backend)

	// Mark backend alive and check status
	pool.MarkBackendStatus(u, true)
	if !backend.IsAlive() {
		t.Errorf("expected backend to be alive after MarkBackendStatus")
	}

	// Mark backend dead and check status
	pool.MarkBackendStatus(u, false)
	if backend.IsAlive() {
		t.Errorf("expected backend to be dead after MarkBackendStatus")
	}

	// Mark with nil URL should do nothing
	pool.MarkBackendStatus(nil, true)
}

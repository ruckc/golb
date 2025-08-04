package golb

import (
	"log"
	"math"
	"sync/atomic"
	"time"
	// Note: No direct dependency on 'Backend' struct fields like 'weight' here,
	// it accesses them via the passed-in slice.
)

// LoadBalancer defines the contract for backend selection strategies.
type LoadBalancer interface {
	// SelectBackend picks the next backend based on the strategy.
	// Implementations should only return backends confirmed to be Alive, or nil if none are available.
	SelectBackend(backends []*Backend) *Backend

	// UpdateResponseTime allows strategies to react to latency measurements.
	// Not all strategies will use this (provide no-op implementations).
	UpdateResponseTime(backend *Backend, duration time.Duration)
}

// --- Round Robin Implementation ---

type RoundRobinBalancer struct {
	current uint64
}

func NewRoundRobinBalancer() LoadBalancer {
	return &RoundRobinBalancer{current: 0}
}

func (r *RoundRobinBalancer) SelectBackend(backends []*Backend) *Backend {
	numBackends := uint64(len(backends))
	if numBackends == 0 {
		return nil
	}
	startIndex := atomic.LoadUint64(&r.current)
	for i := uint64(0); i < numBackends; i++ {
		idx := (startIndex + i) % numBackends
		backend := backends[idx]
		if backend.IsAlive() {
			atomic.StoreUint64(&r.current, (idx+1)%numBackends)
			return backend
		}
	}
	return nil
}

func (r *RoundRobinBalancer) UpdateResponseTime(backend *Backend, duration time.Duration) {}

// --- Least Connections Implementation ---

type LeastConnectionBalancer struct{}

func NewLeastConnectionBalancer() LoadBalancer {
	return &LeastConnectionBalancer{}
}

func (lc *LeastConnectionBalancer) SelectBackend(backends []*Backend) *Backend {
	var selected *Backend = nil
	minConnections := int64(-1)

	for _, backend := range backends {
		if backend.IsAlive() {
			connections := backend.activeConnections.Load()
			if selected == nil || connections < minConnections {
				selected = backend
				minConnections = connections
			}
		}
	}
	// NOTE: The caller MUST handle incrementing/decrementing activeConnections!
	return selected
}

func (lc *LeastConnectionBalancer) UpdateResponseTime(backend *Backend, duration time.Duration) {}

// --- Least Response Time (EWMA) Implementation ---

type LeastResponseTimeBalancer struct {
	alpha float64
}

func NewLeastResponseTimeBalancer(alpha float64) LoadBalancer {
	// Use default if alpha is invalid
	effectiveAlpha := alpha
	if effectiveAlpha <= 0 || effectiveAlpha > 1.0 {
		log.Printf("Warning: Invalid EWMA alpha value (%.2f), using default %.2f.", effectiveAlpha, DefaultEWMAAlpha)
		effectiveAlpha = DefaultEWMAAlpha
	}
	return &LeastResponseTimeBalancer{alpha: effectiveAlpha}
}

func (lrt *LeastResponseTimeBalancer) SelectBackend(backends []*Backend) *Backend {
	var selected *Backend = nil
	minEwma := int64(-1)

	for _, backend := range backends {
		if backend.IsAlive() {
			ewma := backend.ewmaResponseTime.Load()
			// Select if: nothing selected yet OR current EWMA is lower than min (and >0) OR current is 0 and min was >0 (bootstrap)
			if selected == nil || (ewma > 0 && (minEwma <= 0 || ewma < minEwma)) || (ewma == 0 && minEwma > 0) {
				selected = backend
				minEwma = ewma
			}
		}
	}
	return selected
}

func (lrt *LeastResponseTimeBalancer) UpdateResponseTime(backend *Backend, duration time.Duration) {
	if duration < 0 {
		return
	}
	measurement := duration.Nanoseconds()
	if measurement <= 0 {
		measurement = 1 // Use a minimal positive value if measurement is zero or negative
	}

	oldEWMA := backend.ewmaResponseTime.Load()
	var newEWMA int64
	if oldEWMA <= 0 { // Handle initial case or reset
		newEWMA = measurement
	} else {
		newEWMA = int64(lrt.alpha*float64(measurement) + (1.0-lrt.alpha)*float64(oldEWMA))
	}
	if newEWMA <= 0 {
		newEWMA = 1 // Ensure EWMA stays positive
	}
	backend.ewmaResponseTime.Store(newEWMA)
}

// --- Weighted Round Robin (Smooth WRR) Implementation ---

type WeightedRoundRobinBalancer struct{}

func NewWeightedRoundRobinBalancer() LoadBalancer {
	return &WeightedRoundRobinBalancer{}
}

func (w *WeightedRoundRobinBalancer) SelectBackend(backends []*Backend) *Backend {
	var selected *Backend = nil
	maxCurrentWeight := math.MinInt // Use MinInt to correctly handle negative weights if they were allowed (they aren't here)
	totalWeight := 0

	// This pass calculates total weight and finds the backend with highest current weight
	for _, backend := range backends {
		if backend.IsAlive() && backend.weight > 0 {
			backend.stateMutex.Lock()
			backend.currentWeight += backend.weight
			if backend.currentWeight > maxCurrentWeight {
				maxCurrentWeight = backend.currentWeight
				selected = backend
			}
			totalWeight += backend.weight
			backend.stateMutex.Unlock()
		} else if backend.IsAlive() { // Alive but zero or negative weight
			backend.stateMutex.Lock()
			backend.currentWeight = 0 // Reset weight if not participating
			backend.stateMutex.Unlock()
		}
	}

	if selected == nil {
		return nil // No healthy backends with positive weight
	}

	// Adjust the weight of the selected backend for the next round
	selected.stateMutex.Lock()
	selected.currentWeight -= totalWeight
	selected.stateMutex.Unlock()

	return selected
}

func (w *WeightedRoundRobinBalancer) UpdateResponseTime(backend *Backend, duration time.Duration) {}

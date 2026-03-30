package metrics

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Server provides HTTP endpoints for metrics and health checks
type Server struct {
	server  *http.Server
	healthy atomic.Bool
}

// NewServer creates a new metrics server
func NewServer(port int) *Server {
	mux := http.NewServeMux()

	s := &Server{
		server: &http.Server{
			Addr:         fmt.Sprintf(":%d", port),
			Handler:      mux,
			ReadTimeout:  5 * time.Second,
			WriteTimeout: 10 * time.Second,
			IdleTimeout:  60 * time.Second,
		},
	}

	// Prometheus metrics endpoint
	mux.Handle("/metrics", promhttp.Handler())

	// Health check endpoints
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/healthz", s.handleHealth)  // K8s style
	mux.HandleFunc("/ready", s.handleReadiness) // K8s readiness
	mux.HandleFunc("/live", s.handleLiveness)   // K8s liveness

	// Set healthy by default
	s.healthy.Store(true)

	return s
}

// Start starts the metrics server
func (s *Server) Start() error {
	slog.Info("starting metrics server", "addr", s.server.Addr)

	if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("metrics server error: %w", err)
	}

	return nil
}

// Shutdown gracefully shuts down the metrics server
func (s *Server) Shutdown(ctx context.Context) error {
	slog.Info("shutting down metrics server")
	return s.server.Shutdown(ctx)
}

// SetHealthy marks the service as healthy
func (s *Server) SetHealthy(healthy bool) {
	s.healthy.Store(healthy)
}

// handleHealth returns 200 if service is healthy
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	if s.healthy.Load() {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("Unhealthy"))
	}
}

// handleReadiness returns 200 if service is ready to accept traffic
func (s *Server) handleReadiness(w http.ResponseWriter, r *http.Request) {
	// For now, same as health
	// Could check database connectivity, etc.
	s.handleHealth(w, r)
}

// handleLiveness returns 200 if service is alive (not deadlocked)
func (s *Server) handleLiveness(w http.ResponseWriter, _ *http.Request) {
	// Always return OK if we can respond
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}

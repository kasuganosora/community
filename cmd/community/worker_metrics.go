package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/kasuganosora/community/log"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// A dedicated worker has no application HTTP server. Operators may bind its
// existing Prometheus registry to an internal address without starting APIs.
func startWorkerMetrics(address string) (*http.Server, error) {
	if address == "" {
		return nil, nil
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.Handler())
	server := &http.Server{Addr: listener.Addr().String(), Handler: mux, ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Warn("Feed metrics server stopped: %v", err)
		}
	}()
	return server, nil
}

func stopWorkerMetrics(server *http.Server) {
	if server == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		_ = server.Close()
	}
}

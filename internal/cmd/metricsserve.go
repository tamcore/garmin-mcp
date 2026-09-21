package cmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// metricsPath is the one path the metrics listener serves. It is a constant
// rather than a setting: the conventional spelling is what a scraper's default
// configuration looks for, and renaming it buys only obscurity over a response
// a network boundary is already protecting.
const metricsPath = "/metrics"

// metricsShutdownGrace bounds how long a scrape in flight may hold shutdown. A
// scrape is a fast local read, so the grace is short.
const metricsShutdownGrace = 5 * time.Second

// serveMetrics binds address and serves the exposition until ctx ends.
//
// It runs on a listener of its own, never on the MCP mux, for two reasons. The
// endpoint is unauthenticated while every path on that mux is either
// authenticated or deliberately public, and a separate port is what lets an
// operator keep the scrape target off the interface the MCP endpoint publishes.
// Running separately also means it works under stdio, where that mux does not
// exist at all.
func serveMetrics(ctx context.Context, address string, handler http.Handler) error {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("binding the metrics address: %w", err)
	}
	return serveMetricsOn(ctx, listener, handler)
}

// serveMetricsOn is serveMetrics once the listener exists. It is separate so a
// test can supply an ephemeral listener instead of racing for a fixed port.
func serveMetricsOn(ctx context.Context, listener net.Listener, handler http.Handler) error {
	mux := http.NewServeMux()
	mux.Handle(metricsPath, handler)

	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: readHeaderTimeout,
		WriteTimeout:      readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}

	serveErr := make(chan error, 1)
	go func() {
		err := server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serveErr <- err
	}()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}

	// The grace context is detached from the one that just ended: shutting down
	// with an already-cancelled context would skip the grace entirely.
	grace, cancel := context.WithTimeout(context.WithoutCancel(ctx), metricsShutdownGrace)
	defer cancel()
	return errors.Join(server.Shutdown(grace), <-serveErr)
}

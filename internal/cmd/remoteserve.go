package cmd

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/tamcore/garmin-mcp/internal/config"
)

// Server bounds. They are separate from the per-tool timeouts, because these
// protect the process from a peer that opens a connection and then stalls, which
// no handler ever sees.
const (
	// readHeaderTimeout bounds how long a peer may take to send its request
	// headers. A zero value here is the slowloris hole.
	readHeaderTimeout = 15 * time.Second
	// idleTimeout closes a kept-alive connection that carries no request.
	idleTimeout = 2 * time.Minute
	// shutdownGrace bounds the graceful stop: in-flight calls finish, and a
	// stream that will not end does not hold the process open forever.
	shutdownGrace = 20 * time.Second
	// minTLSVersion is the floor for a TLS connection this server terminates.
	minTLSVersion = tls.VersionTLS12
)

// runRemote assembles the multi-user deployment and serves it until the context
// is cancelled.
//
// Two things run side by side: the HTTP server, and the transport's revocation
// watch, which terminates the sessions a withdrawn authorization covers. A
// cancelled context stops both, and it is a graceful stop rather than a failure —
// it is how a supervisor and an interrupt each ask the process to end.
func runRemote(ctx context.Context, cfg config.Config, opts Options) error {
	remote, err := newRemoteDeployment(ctx, cfg, &wiring{
		Logs:    opts.stderr(),
		Tools:   opts.Tools,
		Version: opts.BuildInfo.Version,
	})
	if err != nil {
		return err
	}
	defer func() {
		// A close failure is reported on the diagnostic stream rather than
		// returned, because by this point the serve error is the one an operator
		// needs, and a database that did not close cleanly is a second, lesser
		// fact.
		if closeErr := remote.close(); closeErr != nil {
			// A failing diagnostic stream cannot be reported anywhere else, and
			// the exit status still carries the outcome.
			_, _ = fmt.Fprintf(opts.stderr(), "garmin-mcp: closing the store: %v\n", closeErr)
		}
	}()

	err = remote.serve(ctx)
	if isGracefulStop(ctx, err) {
		return nil
	}
	return err
}

// serve runs the listener and the revocation watch until ctx ends.
//
// The shutdown order is deliberate: the server stops accepting first, so no new
// authorization transaction and no new MCP session can start, and only then are
// in-flight calls given their bounded grace. The store is closed by the caller,
// after both have stopped, because a handler still finishing would otherwise find
// its database gone.
func (r *remoteDeployment) serve(ctx context.Context) error {
	server, err := r.httpServer()
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", r.cfg.BindAddress)
	if err != nil {
		return fmt.Errorf("binding the configured address: %w", err)
	}
	return r.serveOn(ctx, server, listener)
}

// serveOn is serve once the listener exists. It is separate so a test can supply
// an ephemeral listener and exercise the shutdown sequence without racing another
// process for a fixed port.
func (r *remoteDeployment) serveOn(
	ctx context.Context, server *http.Server, listener net.Listener,
) error {
	watch, watchDone := context.WithCancel(ctx)
	defer watchDone()
	revocations := make(chan error, 1)
	go func() { revocations <- r.transport.Run(watch) }()

	// The cleanup shares the watch context, so it stops when the server does and
	// cannot outlive the database it sweeps.
	cleanups := make(chan error, 1)
	go func() { cleanups <- r.cleanup.Run(watch) }()

	serveErr := make(chan error, 1)
	go func() { serveErr <- serveListener(server, listener) }()

	select {
	case err := <-serveErr:
		watchDone()
		return errors.Join(err, <-revocations, <-cleanups)
	case <-ctx.Done():
	}

	stopErr := r.stop(server)
	watchDone()
	return errors.Join(stopErr, <-revocations, <-cleanups, <-serveErr)
}

// httpServer builds the listener's server, with TLS when the operator configured
// a certificate.
//
// A deployment without TLS material is not refused here: the transport already
// refused a cleartext public bind, so what remains is a deployment terminating TLS
// at a trusted proxy, which is a supported shape.
func (r *remoteDeployment) httpServer() (*http.Server, error) {
	server := &http.Server{
		Addr:              r.cfg.BindAddress,
		Handler:           r.handler,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}
	if r.cfg.TLSCertFile == "" {
		return server, nil
	}

	certificate, err := tls.LoadX509KeyPair(r.cfg.TLSCertFile, r.cfg.TLSKeyFile)
	if err != nil {
		// The cause is deliberately not wrapped: a PEM parse error can quote the
		// file's content, and that content is a private key.
		return nil, fmt.Errorf("the configured TLS certificate and key cannot be loaded: %w",
			ErrInsecureDeployment)
	}
	server.TLSConfig = &tls.Config{
		MinVersion:   minTLSVersion,
		Certificates: []tls.Certificate{certificate},
	}
	return server, nil
}

// serveListener serves until the server is closed. A closed server is the
// graceful stop, not a failure.
func serveListener(server *http.Server, listener net.Listener) error {
	var err error
	if server.TLSConfig != nil {
		// The certificate already sits in the TLS configuration, so both paths
		// are empty here by design.
		err = server.ServeTLS(listener, "", "")
	} else {
		err = server.Serve(listener)
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// stop shuts the server down within the grace period, and closes it outright when
// that period runs out, so a stream that will not end cannot hold the process open.
//
// The grace context is deliberately detached from the one that just ended:
// shutting down with an already-cancelled context would skip the grace entirely
// and drop every in-flight call.
func (r *remoteDeployment) stop(server *http.Server) error {
	grace, done := context.WithTimeout(context.Background(), shutdownGrace)
	defer done()

	if err := server.Shutdown(grace); err != nil {
		return errors.Join(err, server.Close())
	}
	return nil
}

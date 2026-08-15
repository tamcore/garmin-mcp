package cmd

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// This file covers the listener half of the remote deployment: the TLS material
// the composition root owns, and the shutdown sequence a stopped process runs.
// The assembly and the login registry are covered in remoteparts_test.go.

// TestRemoteServerUsesConfiguredTLS proves the composition root, not the
// transport, owns TLS: a configured certificate produces a TLS server with a
// modern floor, and unusable material fails closed.
func TestRemoteServerUsesConfiguredTLS(t *testing.T) {
	cfg := remoteConfig(t)
	cfg.TLSCertFile, cfg.TLSKeyFile = writeTestCertificate(t, cfg.StateDir)
	if err := cfg.Validate(); err != nil {
		t.Fatalf("the TLS configuration does not validate: %v", err)
	}
	remote := buildRemote(t, cfg)

	server, err := remote.httpServer()
	if err != nil {
		t.Fatalf("httpServer returned error: %v", err)
	}
	if server.TLSConfig == nil {
		t.Fatal("a configured certificate produced a cleartext server")
	}
	if server.TLSConfig.MinVersion != minTLSVersion {
		t.Errorf("MinVersion = %x, want %x", server.TLSConfig.MinVersion, minTLSVersion)
	}
	if server.ReadHeaderTimeout == 0 {
		t.Error("the server has no read-header timeout, so a stalled peer holds a connection")
	}

	remote.cfg.TLSKeyFile = filepath.Join(cfg.StateDir, "absent.key")
	if _, err := remote.httpServer(); !errors.Is(err, ErrInsecureDeployment) {
		t.Errorf("unusable TLS material returned %v, want ErrInsecureDeployment", err)
	}
}

// TestRemoteServeStopsOnACancelledContext is the graceful-shutdown guarantee: the
// listener stops accepting, the revocation watch ends with it, and serve returns
// without an error a supervisor would read as a crash.
func TestRemoteServeStopsOnACancelledContext(t *testing.T) {
	remote := buildRemote(t, remoteConfig(t))

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	address := listener.Addr().String()

	ctx, stop := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- remote.serveOn(ctx, &http.Server{Handler: remote.handler}, listener) }()

	// The listener is live before the stop, so this exercises a real shutdown
	// rather than a server that never started.
	if conn, dialErr := net.DialTimeout("tcp", address, 2*time.Second); dialErr == nil {
		_ = conn.Close()
	}
	stop()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("serveOn returned %v, want nil for a cancelled context", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("serveOn did not return after its context was cancelled")
	}

	if conn, dialErr := net.DialTimeout("tcp", address, time.Second); dialErr == nil {
		_ = conn.Close()
		t.Error("the listener still accepts connections after the shutdown")
	}
}

// TestRunRemoteServesAndStopsCleanly drives the command's own entry point: it
// assembles the deployment, binds the configured address, answers a request, and
// returns without an error when its context ends — which is how a supervisor and
// an interrupt both stop the process.
func TestRunRemoteServesAndStopsCleanly(t *testing.T) {
	cfg := remoteConfig(t)
	cfg.BindAddress = freeLoopbackAddress(t)
	if err := cfg.Validate(); err != nil {
		t.Fatalf("the configuration does not validate: %v", err)
	}

	ctx, stop := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- runRemote(ctx, cfg, Options{Stdout: io.Discard, Stderr: io.Discard})
	}()

	waitForListener(t, cfg.BindAddress)
	stop()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("runRemote returned %v, want nil for a cancelled context", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("runRemote did not return after its context was cancelled")
	}
}

// freeLoopbackAddress reserves and releases a loopback port, so the test binds an
// address no other test in the package is using.
func freeLoopbackAddress(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release the reserved port: %v", err)
	}
	return address
}

// waitForListener blocks until the address accepts a connection, so the shutdown
// under test is a shutdown of a running server rather than of one that never
// started.
func waitForListener(t *testing.T, address string) {
	t.Helper()

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", address, time.Second)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("nothing accepted a connection on %s", address)
}

// writeTestCertificate writes a throwaway self-signed certificate and key into
// dir. It is generated rather than committed, because a private key in a
// repository is a private key on every developer's disk.
func writeTestCertificate(t *testing.T, dir string) (certPath, keyPath string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	encodedKey, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}

	certPath = filepath.Join(dir, "tls.crt")
	keyPath = filepath.Join(dir, "tls.key")
	writePEM(t, certPath, "CERTIFICATE", der)
	writePEM(t, keyPath, "EC PRIVATE KEY", encodedKey)
	return certPath, keyPath
}

func writePEM(t *testing.T, path, blockType string, der []byte) {
	t.Helper()

	encoded := pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der})
	if err := os.WriteFile(path, encoded, os.FileMode(0o600)); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

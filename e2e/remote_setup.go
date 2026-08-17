//go:build e2e

package e2e

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// This file builds the synthetic remote deployment every test in this package
// drives: a generated certificate, a generated master key, an empty database,
// and one preregistered public OAuth client. Two configuration rules decide
// that shape and are worked with rather than around — the transport refuses a
// cleartext public bind, and the authorization server requires an https issuer
// — so the listener terminates TLS on a loopback address with a certificate
// this file signs.

const (
	// remoteReadyTimeout bounds how long the listener may take to answer.
	remoteReadyTimeout = 30 * time.Second
	// remotePollInterval is the gap between readiness probes.
	remotePollInterval = 100 * time.Millisecond
	// remoteRequestTimeout bounds one probe.
	remoteRequestTimeout = 10 * time.Second
	// remoteClientID is the preregistered OAuth client this deployment knows.
	remoteClientID = "e2e-client"
	// remoteScope is the single scope the deployment offers.
	remoteScope = "garmin:read"
	// metadataPath is where RFC 9728 protected resource metadata is served.
	metadataPath = "/.well-known/oauth-protected-resource"
)

// A remoteServer is a running deployment plus the client that trusts it.
type remoteServer struct {
	origin string
	mcpURL string
	client *http.Client
	// stateDir is where the deployment's database and master key live. A test
	// that needs to reopen the database after the subprocess has stopped —
	// the store is single-writer, so nothing may reopen it while the process
	// still holds it — uses this together with stop.
	stateDir string
	// stop kills the subprocess and waits for it to exit. It is safe to call
	// more than once and safe to never call explicitly: the same stop also
	// runs at test cleanup.
	stop func()
}

// metadataURL is the absolute URL of the RFC 9728 document.
func (s remoteServer) metadataURL() string { return s.origin + metadataPath }

// mcpURLFor is the MCP endpoint URL for a deployment's origin. It exists so a
// seed callback, which runs before remoteServer exists, can compute the exact
// resource string a code or consent must be bound to.
func mcpURLFor(origin string) string { return origin + "/mcp" }

// startRemoteServer brings up the binary in remote mode and returns once the
// listener answers. Everything it needs is generated in a private directory,
// and the process is stopped when the test ends.
func startRemoteServer(t *testing.T) remoteServer {
	t.Helper()
	return startRemoteServerSeeded(t, nil)
}

// startRemoteServerSeeded is startRemoteServer with one extra step: seed, when
// non-nil, runs against the state directory after the master key and the
// database-bearing configuration exist but before the binary is started.
//
// That is the seam a test uses to install rows the running process will read —
// a principal, an authorization code — without a browser or a Garmin login,
// both of which this package's tests must never attempt. seed receives the
// state directory and the origin, which is what a caller needs to open the same
// database and mint records bound to the same resource the deployment serves.
func startRemoteServerSeeded(t *testing.T, seed func(dir, origin string)) remoteServer {
	t.Helper()
	return startRemoteServerConfigured(t, "", seed)
}

// startRemoteServerConfigured is startRemoteServerSeeded with the outbound
// proxy under the caller's control: an empty proxyURL keeps the default
// blackhole, so Garmin traffic can never leave the process; a non-empty one —
// a recording, always-refusing CONNECT proxy — lets a test observe exactly what
// a login attempt asked to reach, without any request to it ever succeeding.
func startRemoteServerConfigured(
	t *testing.T, proxyURL string, seed func(dir, origin string),
) remoteServer {
	t.Helper()

	dir := stateDir(t)
	port := freePort(t)
	certPEM := writeTLSMaterial(t, dir)
	origin := fmt.Sprintf("https://127.0.0.1:%d", port)

	writeMasterKey(t, dir)
	configPath := writeRemoteConfig(t, dir, port, origin)
	if seed != nil {
		seed(dir, origin)
	}

	server := remoteServer{
		origin:   origin,
		mcpURL:   mcpURLFor(origin),
		client:   trustingClient(t, certPEM),
		stateDir: dir,
	}
	server.stop = launchRemote(t, dir, configPath, proxyURL)
	waitForRemote(t, server)
	return server
}

// launchRemote starts the server process and returns a function that stops
// it. The same function also runs at test cleanup, guarded by sync.Once, so a
// test that stops the deployment early to reopen its database — the store is
// single-writer, so nothing may reopen it while the process still holds it —
// does not stop it a second time, and a test that never calls it explicitly
// still gets a clean shutdown. Its diagnostic stream is reported only on
// failure, because it is the only place a startup refusal is explained. An
// empty proxyURL keeps the default blackhole.
func launchRemote(t *testing.T, dir, configPath, proxyURL string) func() {
	t.Helper()

	bin := buildBinary(t)
	logPath := filepath.Join(dir, "server.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create the server log: %v", err)
	}

	cmd := offlineCommandWithProxy(bin, proxyURL, "serve", "--config", configPath)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the server: %v", err)
	}

	var once sync.Once
	stop := func() {
		once.Do(func() {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		})
	}
	t.Cleanup(func() {
		stop()
		_ = logFile.Close()
		if t.Failed() {
			if contents, readErr := os.ReadFile(logPath); readErr == nil {
				t.Logf("server log:\n%s", contents)
			}
		}
	})
	return stop
}

// waitForRemote polls the metadata document until the deployment answers.
func waitForRemote(t *testing.T, server remoteServer) {
	t.Helper()

	deadline := time.Now().Add(remoteReadyTimeout)
	for time.Now().Before(deadline) {
		response, err := server.client.Get(server.metadataURL())
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(remotePollInterval)
	}
	t.Fatalf("the deployment did not answer on %s within %s", server.metadataURL(), remoteReadyTimeout)
}

// freePort reserves a port, releases it, and returns it. The window between the
// two is why the deployment binds loopback only.
func freePort(t *testing.T) int {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("release the reserved port: %v", err)
	}
	return port
}

// writeTLSMaterial generates a self-signed certificate for 127.0.0.1 and writes
// the pair into dir. It returns the certificate in PEM form, which is the only
// trust anchor the test client is given.
func writeTLSMaterial(t *testing.T, dir string) []byte {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate the TLS key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "garmin-mcp e2e"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("sign the certificate: %v", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	writeFile(t, filepath.Join(dir, "tls.crt"), certPEM)
	writeFile(t, filepath.Join(dir, "tls.key"), encodeKey(t, key))
	return certPEM
}

// encodeKey renders the private key as PKCS#8 PEM.
func encodeKey(t *testing.T, key *ecdsa.PrivateKey) []byte {
	t.Helper()

	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("encode the TLS key: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

// writeMasterKey installs a synthetic owner-only master key. The value is
// random and lives only for this test; nothing it protects outlives the run.
func writeMasterKey(t *testing.T, dir string) {
	t.Helper()

	material := make([]byte, 32)
	if _, err := rand.Read(material); err != nil {
		t.Fatalf("generate the master key: %v", err)
	}
	document, err := json.Marshal(map[string]any{
		"version": 1,
		"key":     base64.StdEncoding.EncodeToString(material),
	})
	if err != nil {
		t.Fatalf("encode the master key document: %v", err)
	}
	writeFile(t, filepath.Join(dir, "key-v1.json"), document)
}

// writeRemoteConfig writes the deployment configuration and returns its path.
func writeRemoteConfig(t *testing.T, dir string, port int, origin string) string {
	t.Helper()

	resource := origin + "/mcp"
	document := strings.Join([]string{
		"transport: streamable-http",
		fmt.Sprintf("bind-address: 127.0.0.1:%d", port),
		"public-url: " + resource,
		"tls-cert-file: " + filepath.Join(dir, "tls.crt"),
		"tls-key-file: " + filepath.Join(dir, "tls.key"),
		"state-dir: " + dir,
		"master-key-file: " + filepath.Join(dir, "key-v1.json"),
		"database-path: " + filepath.Join(dir, "garmin.db"),
		"oauth-clients:",
		"  - id: " + remoteClientID,
		"    name: " + remoteClientName,
		"    redirect-uris:",
		"      - http://127.0.0.1:33418/callback",
		"    scopes:",
		"      - " + remoteScope,
		"    resources:",
		"      - " + resource,
		"    public: true",
		"",
	}, "\n")

	path := filepath.Join(dir, "config.yaml")
	writeFile(t, path, []byte(document))
	return path
}

// ownerOnly is the mode every file this test writes gets. Key material and a
// certificate key are among them, so the mode is not a per-call decision.
const ownerOnly os.FileMode = 0o600

// writeFile writes contents owner-only and fails the test otherwise.
func writeFile(t *testing.T, path string, contents []byte) {
	t.Helper()

	if err := os.WriteFile(path, contents, ownerOnly); err != nil {
		t.Fatalf("write %s: %v", filepath.Base(path), err)
	}
}

// trustingClient returns a client that trusts exactly the generated
// certificate. The system pool is deliberately not used: the deployment's
// identity for this run is the certificate the test just signed.
func trustingClient(t *testing.T, certPEM []byte) *http.Client {
	t.Helper()

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certPEM) {
		t.Fatal("the generated certificate was not accepted into the trust pool")
	}
	return &http.Client{
		Timeout: remoteRequestTimeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		},
	}
}

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
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This file exercises the remote Streamable HTTP transport the way a client
// meets it before it holds a token: it discovers the protected resource
// metadata, receives the RFC 6750 challenge, and is refused.
//
// The deployment is synthetic and created here: a generated certificate, a
// generated master key, an empty database, and one preregistered public OAuth
// client. Two configuration rules decide that shape and are worked with rather
// than around — the transport refuses a cleartext public bind, and the
// authorization server requires an https issuer — so the listener terminates
// TLS on a loopback address with a certificate this test signs.
//
// Completing the authorization code flow needs a Garmin login, which is out of
// scope here. Everything up to the point where a token would be presented is
// not, and that is what these tests cover.

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
}

// metadataURL is the absolute URL of the RFC 9728 document.
func (s remoteServer) metadataURL() string { return s.origin + metadataPath }

// startRemoteServer brings up the binary in remote mode and returns once the
// listener answers. Everything it needs is generated in a private directory,
// and the process is stopped when the test ends.
func startRemoteServer(t *testing.T) remoteServer {
	t.Helper()

	dir := stateDir(t)
	port := freePort(t)
	certPEM := writeTLSMaterial(t, dir)
	origin := fmt.Sprintf("https://127.0.0.1:%d", port)

	writeMasterKey(t, dir)
	configPath := writeRemoteConfig(t, dir, port, origin)

	server := remoteServer{
		origin: origin,
		mcpURL: origin + "/mcp",
		client: trustingClient(t, certPEM),
	}
	launchRemote(t, dir, configPath)
	waitForRemote(t, server)
	return server
}

// launchRemote starts the server process and stops it when the test ends. Its
// diagnostic stream is reported only on failure, because it is the only place a
// startup refusal is explained.
func launchRemote(t *testing.T, dir, configPath string) {
	t.Helper()

	bin := buildBinary(t)
	logPath := filepath.Join(dir, "server.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create the server log: %v", err)
	}

	cmd := exec.Command(bin, "serve", "--config", configPath)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the server: %v", err)
	}

	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = logFile.Close()
		if t.Failed() {
			if contents, readErr := os.ReadFile(logPath); readErr == nil {
				t.Logf("server log:\n%s", contents)
			}
		}
	})
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
		"    name: End-to-end client",
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

// TestRemoteTransportServesProtectedResourceMetadata covers the first step a
// client takes: the RFC 9728 document is readable without a token, names the
// canonical resource, and advertises the header as the only place a bearer
// token is accepted.
func TestRemoteTransportServesProtectedResourceMetadata(t *testing.T) {
	server := startRemoteServer(t)

	response, err := server.client.Get(server.metadataURL())
	if err != nil {
		t.Fatalf("read the protected resource metadata: %v", err)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: the document must be readable unauthenticated", response.StatusCode)
	}

	var metadata struct {
		Resource               string   `json:"resource"`
		AuthorizationServers   []string `json:"authorization_servers"`
		ScopesSupported        []string `json:"scopes_supported"`
		BearerMethodsSupported []string `json:"bearer_methods_supported"`
	}
	if err := json.NewDecoder(response.Body).Decode(&metadata); err != nil {
		t.Fatalf("decode the protected resource metadata: %v", err)
	}

	if metadata.Resource != server.mcpURL {
		t.Errorf("resource = %q, want %q", metadata.Resource, server.mcpURL)
	}
	if len(metadata.AuthorizationServers) == 0 {
		t.Error("authorization_servers is empty; a client cannot start the flow")
	}
	if !contains(metadata.ScopesSupported, remoteScope) {
		t.Errorf("scopes_supported = %v, want it to contain %q", metadata.ScopesSupported, remoteScope)
	}
	if len(metadata.BearerMethodsSupported) != 1 || metadata.BearerMethodsSupported[0] != "header" {
		t.Errorf("bearer_methods_supported = %v, want [header] only", metadata.BearerMethodsSupported)
	}
}

// TestRemoteTransportRefusesAnMCPRequestWithoutABearerToken covers the second
// step: the request is refused, and the challenge points back at the document
// the previous test read. A request that presented no credential must not be
// reported as one that presented an invalid one.
func TestRemoteTransportRefusesAnMCPRequestWithoutABearerToken(t *testing.T) {
	server := startRemoteServer(t)

	response := postInitialize(t, server, nil)
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for a request with no bearer token", response.StatusCode)
	}

	challenge := response.Header.Get("WWW-Authenticate")
	if !strings.HasPrefix(challenge, "Bearer ") {
		t.Fatalf("WWW-Authenticate = %q, want an RFC 6750 Bearer challenge", challenge)
	}
	if !strings.Contains(challenge, `resource_metadata="`+server.metadataURL()+`"`) {
		t.Errorf("challenge %q does not point at %s", challenge, server.metadataURL())
	}
	if strings.Contains(challenge, "error=") {
		t.Errorf("challenge %q reports an error; a missing token is not an invalid one", challenge)
	}
	if store := response.Header.Get("Cache-Control"); store != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", store)
	}
}

// TestRemoteTransportRefusesABearerTokenOutsideTheHeader covers the rule that a
// token is read from the Authorization header and from nowhere else: a query
// parameter carrying one leaves the request unauthenticated, and the challenge
// is the bare one, because nothing was presented where it counts.
func TestRemoteTransportRefusesABearerTokenOutsideTheHeader(t *testing.T) {
	server := startRemoteServer(t)

	request := newInitializeRequest(t, server.mcpURL+"?access_token=synthetic-e2e-token")
	response, err := server.client.Do(request)
	if err != nil {
		t.Fatalf("send the MCP request: %v", err)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: a query parameter never authenticates", response.StatusCode)
	}
	if challenge := response.Header.Get("WWW-Authenticate"); strings.Contains(challenge, "error=") {
		t.Errorf("challenge %q treats a query parameter as a presented credential", challenge)
	}
}

// TestRemoteTransportReportsAnUnusableBearerTokenAsInvalid is the other half of
// the distinction: a credential was presented in the right place and rejected,
// so the challenge says so and a client knows not to retry the same token.
func TestRemoteTransportReportsAnUnusableBearerTokenAsInvalid(t *testing.T) {
	server := startRemoteServer(t)

	response := postInitialize(t, server, map[string]string{
		"Authorization": "Bearer synthetic-e2e-token",
	})
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for an unusable token", response.StatusCode)
	}
	challenge := response.Header.Get("WWW-Authenticate")
	if !strings.Contains(challenge, `error="invalid_token"`) {
		t.Errorf("challenge %q does not report invalid_token", challenge)
	}
}

// postInitialize sends an MCP initialize call with the given extra headers.
func postInitialize(t *testing.T, server remoteServer, headers map[string]string) *http.Response {
	t.Helper()

	request := newInitializeRequest(t, server.mcpURL)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := server.client.Do(request)
	if err != nil {
		t.Fatalf("send the MCP request: %v", err)
	}
	return response
}

// newInitializeRequest builds a well-formed Streamable HTTP initialize request,
// so a refusal can only be about authorization.
func newInitializeRequest(t *testing.T, url string) *http.Request {
	t.Helper()

	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":` +
		`{"protocolVersion":"2026-07-28","capabilities":{},` +
		`"clientInfo":{"name":"garmin-mcp-e2e","version":"0.0.0"}}}`

	request, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build the MCP request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("MCP-Protocol-Version", "2026-07-28")
	return request
}

// contains reports whether values holds want.
func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

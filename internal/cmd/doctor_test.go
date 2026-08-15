package cmd_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/cmd"
	"github.com/tamcore/garmin-mcp/internal/cryptostore"
)

// tierReadOnly is the tier label every report prints.
const tierReadOnly = "read-only"

// The synthetic secret material doctor must never print.
const (
	sentinelKeyMaterial   = "bWFzdGVyLWtleS1zZW50aW5lbA=="
	sentinelTokenDocument = `{"di_token":"sentinel-di-token",` +
		`"di_refresh_token":"sentinel-refresh-token","di_client_id":"sentinel-client"}`
)

// runDoctor executes the command and reports both streams and the exit code.
func runDoctor(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()

	var out, errOut bytes.Buffer
	code = cmd.Execute(context.Background(), cmd.Options{
		BuildInfo: cmd.BuildInfo{Version: testVersion, Commit: testCommit},
		Args:      append([]string{cmdDoctor}, args...),
		Stdout:    &out,
		Stderr:    &errOut,
	})
	return out.String(), errOut.String(), code
}

// remoteDoctorEnv points doctor at a synthetic remote deployment and returns the
// database path it created. The database file is empty: doctor reports its mode
// and never opens it, because a diagnostic that opened a database would migrate
// the very thing it was asked to inspect.
func remoteDoctorEnv(t *testing.T) string {
	t.Helper()

	dir := stateDir(t)
	database := filepath.Join(dir, "state.db")
	if err := os.WriteFile(database, nil, 0o600); err != nil {
		t.Fatalf("create the database file: %v", err)
	}

	t.Setenv("GARMIN_MCP_TRANSPORT", "streamable-http")
	t.Setenv("GARMIN_MCP_PUBLIC_URL", "https://mcp.example.test/mcp")
	t.Setenv("GARMIN_MCP_BIND_ADDRESS", "127.0.0.1:8443")
	t.Setenv("GARMIN_MCP_DATABASE_PATH", database)
	t.Setenv("GARMIN_MCP_MASTER_KEY_FILE", filepath.Join(dir, "keys", "key-v1.json"))
	t.Setenv("GARMIN_MCP_TLS_CERT_FILE", filepath.Join(dir, "tls.crt"))
	t.Setenv("GARMIN_MCP_TLS_KEY_FILE", filepath.Join(dir, "tls.key"))
	t.Setenv("GARMIN_MCP_OAUTH_CLIENTS", `[{"id":"example-client",`+
		`"name":"Example MCP client",`+
		`"redirect-uris":["https://client.example.test/callback"],`+
		`"scopes":["garmin:read"],`+
		`"resources":["https://mcp.example.test/mcp"],`+
		`"public":true}]`)
	return database
}

// TestDoctorReportsTheRemoteDeployment covers the remote half of the report: an
// operator learns the transport, the canonical public URL, whether TLS is
// terminated here, where the database is and whether another local account can
// read it, and which clients are registered — and learns none of their secrets.
func TestDoctorReportsTheRemoteDeployment(t *testing.T) {
	clearGarminEnv(t)
	database := remoteDoctorEnv(t)

	stdout, stderr, code := runDoctor(t)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty: the report is the result", stderr)
	}

	for _, want := range []string{
		"streamable-http",
		"https://mcp.example.test/mcp",
		"tls",
		database,
		"owner-only",
		"example-client",
		tierReadOnly,
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("report does not mention %q:\n%s", want, stdout)
		}
	}
}

// TestDoctorReportsAnAbsentDatabase keeps a fresh deployment from reading as a
// broken one: the database does not exist until the first serve run, and that is
// informational rather than a failure.
func TestDoctorReportsAnAbsentDatabase(t *testing.T) {
	clearGarminEnv(t)
	database := remoteDoctorEnv(t)
	if err := os.Remove(database); err != nil {
		t.Fatalf("remove the database file: %v", err)
	}

	stdout, _, code := runDoctor(t)
	if code != 0 {
		t.Errorf("exit code = %d, want 0 for a deployment that has not started yet", code)
	}
	if !strings.Contains(stdout, "absent") {
		t.Errorf("report does not say the database is absent:\n%s", stdout)
	}
}

// TestDoctorReportsAWorldReadableDatabase is the check that has teeth: a database
// another local account can read holds every principal's encrypted tokens, and the
// command must fail rather than report a healthy deployment.
func TestDoctorReportsAWorldReadableDatabase(t *testing.T) {
	clearGarminEnv(t)
	database := remoteDoctorEnv(t)
	if err := os.Chmod(database, 0o644); err != nil {
		t.Fatalf("relax the database mode: %v", err)
	}

	stdout, _, code := runDoctor(t)
	if code == 0 {
		t.Error("exit code = 0 for a database another local account can read")
	}
	if !strings.Contains(stdout, "not owner-only") {
		t.Errorf("report does not name the unsafe database:\n%s", stdout)
	}
}

// TestDoctorReportsTheEffectiveDeploymentOnStdout is the behavior: doctor answers
// the questions an operator has before starting a server — which transport, which
// principal, where the key and the store are, and which tool tiers are enabled —
// and it answers them on the result stream, because a diagnostic report is this
// command's result and it never starts an MCP session.
func TestDoctorReportsTheEffectiveDeploymentOnStdout(t *testing.T) {
	clearGarminEnv(t)
	dir := stateDir(t)

	stdout, stderr, code := runDoctor(t)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty: the report is the result", stderr)
	}

	for _, want := range []string{
		"transport", "stdio",
		"principal", "local",
		"encryption key",
		"token store",
		filepath.Join(dir, "keys"),
		filepath.Join(dir, "tokens"),
		tierReadOnly,
		"write",
		"destructive",
		"garmin.com",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("report does not mention %q:\n%s", want, stdout)
		}
	}
}

// TestDoctorPrintsTheRedactedConfiguration keeps the effective-configuration dump
// on the one code path that cannot leak: the redacted representation config already
// owns.
func TestDoctorPrintsTheRedactedConfiguration(t *testing.T) {
	clearGarminEnv(t)
	stateDir(t)
	t.Setenv("GARMIN_MCP_MASTER_KEY", sentinelKeyMaterial)
	t.Setenv("GARMIN_MCP_GARMIN_TOKENS", sentinelTokenDocument)

	stdout, stderr, _ := runDoctor(t)

	for _, forbidden := range []string{
		sentinelKeyMaterial,
		"sentinel-di-token",
		"sentinel-refresh-token",
		"sentinel-client",
	} {
		if strings.Contains(stdout, forbidden) {
			t.Errorf("stdout leaked %q:\n%s", forbidden, stdout)
		}
		if strings.Contains(stderr, forbidden) {
			t.Errorf("stderr leaked %q:\n%s", forbidden, stderr)
		}
	}
	if !strings.Contains(stdout, "config.Config{") {
		t.Errorf("report does not print the effective configuration:\n%s", stdout)
	}
}

// TestDoctorReportsAnAbsentKeyWithoutCreatingOne keeps the command read-only. A
// diagnostic that provisions key material on the way past would make "the key is
// missing" unobservable.
func TestDoctorReportsAnAbsentKeyWithoutCreatingOne(t *testing.T) {
	clearGarminEnv(t)
	dir := stateDir(t)

	stdout, _, code := runDoctor(t)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 for a deployment that has not been set up", code)
	}
	if !strings.Contains(stdout, "absent") {
		t.Errorf("report does not say the key is absent:\n%s", stdout)
	}

	if _, err := os.Stat(filepath.Join(dir, "keys")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("doctor created the key directory: stat error = %v", err)
	}
}

// TestDoctorFailsOnUnsafeKeyPermissions is the fail-closed half: an operator who
// runs doctor must learn that key material another local account can read is
// treated as compromised, and the command must exit non-zero so a script notices.
func TestDoctorFailsOnUnsafeKeyPermissions(t *testing.T) {
	clearGarminEnv(t)
	dir := stateDir(t)

	keys := filepath.Join(dir, "keys")
	if err := os.MkdirAll(keys, 0o700); err != nil {
		t.Fatalf("create the key directory: %v", err)
	}
	keyFile := filepath.Join(keys, "key-v1.json")
	if err := os.WriteFile(keyFile,
		[]byte(`{"version":1,"key":"`+strings.Repeat("A", 43)+`="}`), 0o644); err != nil {
		t.Fatalf("write the key file: %v", err)
	}

	stdout, stderr, code := runDoctor(t)
	if code == 0 {
		t.Error("exit code = 0 for a group-readable key file")
	}
	if !strings.Contains(stdout+stderr, "owner-only") {
		t.Errorf("neither stream explains the refusal:\nstdout=%s\nstderr=%s", stdout, stderr)
	}
}

// TestDoctorReportsAnUnlinkedAccountOnceTheKeyAndStoreExist covers the check that
// only runs when everything before it is in place: with usable key material and an
// owner-only store, doctor reads the bound principal's record and reports that the
// account has not been linked yet, without creating a record of its own.
func TestDoctorReportsAnUnlinkedAccountOnceTheKeyAndStoreExist(t *testing.T) {
	clearGarminEnv(t)
	dir := stateDir(t)

	if _, err := cryptostore.LoadOrCreateKey(filepath.Join(dir, "keys"), 1); err != nil {
		t.Fatalf("creating the key: %v", err)
	}
	tokens := filepath.Join(dir, "tokens")
	if err := os.MkdirAll(tokens, 0o700); err != nil {
		t.Fatalf("creating the token directory: %v", err)
	}

	stdout, _, code := runDoctor(t)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 for a set-up deployment with no linked account", code)
	}

	if !strings.Contains(stdout, "garmin-mcp auth") {
		t.Errorf("report does not tell the operator how to link the account:\n%s", stdout)
	}
	records, err := os.ReadDir(filepath.Join(tokens, "tokens"))
	if err != nil {
		t.Fatalf("reading the record directory: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("doctor wrote %d records for an account nobody linked", len(records))
	}
}

// TestDoctorReportsAStoreThatIsNotADirectory covers the shape check: a file where
// the store belongs is refused rather than opened.
func TestDoctorReportsAStoreThatIsNotADirectory(t *testing.T) {
	clearGarminEnv(t)
	dir := stateDir(t)

	if err := os.WriteFile(filepath.Join(dir, "tokens"), nil, 0o600); err != nil {
		t.Fatalf("creating the file in the store's place: %v", err)
	}

	stdout, stderr, code := runDoctor(t)
	if code == 0 {
		t.Error("exit code = 0 for a token store that is not a directory")
	}
	if !strings.Contains(stdout+stderr, "not a directory") {
		t.Errorf("neither stream explains the refusal:\nstdout=%s\nstderr=%s", stdout, stderr)
	}
}

// TestDoctorReportsTheToolTiers covers the operator half of the tier gate, which is
// the part doctor can answer without a granted scope.
func TestDoctorReportsTheToolTiers(t *testing.T) {
	clearGarminEnv(t)
	stateDir(t)

	stdout, _, code := runDoctor(t, "--enable-write-tools")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout, "write: enabled") {
		t.Errorf("report does not show the enabled write tier:\n%s", stdout)
	}
	if !strings.Contains(stdout, "destructive: disabled") {
		t.Errorf("report does not show the disabled destructive tier:\n%s", stdout)
	}
	if !strings.Contains(stdout, "scope") {
		t.Errorf("report does not say enablement alone grants nothing:\n%s", stdout)
	}
}

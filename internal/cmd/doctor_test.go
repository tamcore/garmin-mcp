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
)

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
		"read-only",
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

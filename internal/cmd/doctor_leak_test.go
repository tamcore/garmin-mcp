package cmd

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/config"
)

// The synthetic material the report must never carry, under any rendering.
const (
	leakKeyMaterial = "bWFzdGVyLWtleS1zZW50aW5lbA=="
	leakTokenJSON   = `{"di_token":"sentinel-di-token","di_refresh_token":"sentinel-refresh"}`
)

// strippedReport is the report with its methods removed. An alias drops the method
// set, so a String or LogValue implementation cannot hide a field from fmt: what
// remains is what reflection sees, which is what a reflective logger would print.
type strippedReport diagnosis

// reportWithSecrets builds a report for a configuration carrying both inline
// secrets, which is the worst case a doctor run can be handed.
func reportWithSecrets(t *testing.T) diagnosis {
	t.Helper()

	cfg := localConfig(t)
	cfg.MasterKey = config.NewSecret(leakKeyMaterial)
	cfg.GarminTokens = config.NewSecret(leakTokenJSON)

	report, err := diagnose(t.Context(), cfg)
	if err != nil {
		t.Fatalf("diagnose returned error: %v", err)
	}
	return report
}

// TestDiagnosisCarriesNoSecretUnderAnyVerb strips the report's methods and prints it
// with every fmt verb, including the Go-syntax verb that reflects over the value and
// the verbs a type does not support, whose badVerb path re-prints the value at depth
// zero.
func TestDiagnosisCarriesNoSecretUnderAnyVerb(t *testing.T) {
	report := reportWithSecrets(t)
	stripped := strippedReport(report)

	values := map[string]any{
		"report":   report,
		"stripped": stripped,
		"pointer":  &stripped,
	}
	for _, verb := range []string{"%v", "%+v", "%s", "%q", "%#v", "%d", "%x", "%T"} {
		for name, value := range values {
			rendered := fmt.Sprintf(verb, value)
			for _, material := range []string{leakKeyMaterial, "sentinel-di-token", "sentinel-refresh"} {
				if strings.Contains(rendered, material) {
					t.Errorf("%s rendered with %s leaked %q: %s", name, verb, material, rendered)
				}
			}
		}
	}
}

// TestDiagnosisLoggedThroughSlogCarriesNoSecret keeps the structured-logging path
// honest as well: a handler walks a value's exported fields, so a field holding
// secret material would reach a log record even though no String method printed it.
func TestDiagnosisLoggedThroughSlogCarriesNoSecret(t *testing.T) {
	report := reportWithSecrets(t)

	var sink bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&sink, nil))
	logger.Info("diagnosis",
		slog.Any("report", report),
		slog.Any("stripped", strippedReport(report)))

	for _, material := range []string{leakKeyMaterial, "sentinel-di-token", "sentinel-refresh"} {
		if strings.Contains(sink.String(), material) {
			t.Errorf("the log record leaked %q: %s", material, sink.String())
		}
	}
}

// TestRemoteDiagnosisCarriesNoClientSecret covers the registry half of the report.
// A client's secret digest is the value an attacker would want in order to test a
// guessed secret offline, so it must not reach the report under any rendering,
// including the one an alias-stripped value gives a reflective logger.
func TestRemoteDiagnosisCarriesNoClientSecret(t *testing.T) {
	const digest = "5e884898da28047151d0e56f8dc6292773603d0d6aabbdd62a11ef721d1542d8"

	cfg := remoteConfig(t)
	digestPath := filepath.Join(cfg.StateDir, "client.sha256")
	if err := os.WriteFile(digestPath, []byte(digest), 0o600); err != nil {
		t.Fatalf("write the digest file: %v", err)
	}
	cfg.OAuthClients[0].Public = false
	cfg.OAuthClients[0].SecretHashPath = digestPath

	report, err := diagnose(t.Context(), cfg)
	if err != nil {
		t.Fatalf("diagnose returned error: %v", err)
	}
	if !strings.Contains(report.render(), cfg.OAuthClients[0].ID) {
		t.Error("the report does not name the registered client, so it says nothing useful")
	}

	stripped := strippedReport(report)
	for _, verb := range []string{"%v", "%+v", "%s", "%#v"} {
		for _, value := range []any{report, stripped, &stripped, report.render()} {
			if rendered := fmt.Sprintf(verb, value); strings.Contains(rendered, digest) {
				t.Errorf("a %s rendering leaked the client secret digest: %s", verb, rendered)
			}
		}
	}
}

// TestDiagnosisHasNoSecretBearingField walks the report reflectively and fails on any
// field that could hold secret material at all. It is the structural version of the
// two tests above: they prove this value does not leak, this one proves no future
// field can.
func TestDiagnosisHasNoSecretBearingField(t *testing.T) {
	secretType := reflect.TypeFor[config.Secret]()

	for _, field := range reflect.VisibleFields(reflect.TypeFor[diagnosis]()) {
		if field.Type == secretType {
			t.Errorf("diagnosis field %q holds config.Secret; a report must carry no secret",
				field.Name)
		}
	}
}

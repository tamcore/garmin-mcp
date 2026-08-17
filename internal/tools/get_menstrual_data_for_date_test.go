package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// Every fixture across the women's-health tests in this package is synthetic: no
// cycle phase, symptom, note or date is a recording of a real account. This is the
// most sensitive category this project handles, so every fixture below is
// deliberately invented rather than borrowed from any real sample.

// womensHealthTestDate is the calendar day every women's-health test in this
// package asks for.
const womensHealthTestDate = "2026-01-31"

// womensHealthRegistrar registers exactly the three women's-health tools.
//
// It exists because these tools are not yet listed in register.go, so the shared
// harness (newToolHarness) cannot reach them: wiring register.go is out of scope
// for this slice. It drives the real registration functions through the real
// server, which is what the shared harness does for the tools that are listed,
// following the same pattern get_hill_score_test.go's scoresRegistrar and
// badgechallengelists_test.go's challengesRegistrar already establish for tools
// ahead of their own wiring.
type womensHealthRegistrar struct {
	svc *service
}

// womensHealthRegistrations is the list register.go must grow, in the same order.
func womensHealthRegistrations() []registration {
	return []registration{
		{getMenstrualCalendarDataContract, registerGetMenstrualCalendarData},
		{getMenstrualDataForDateContract, registerGetMenstrualDataForDate},
		{getPregnancySummaryContract, registerGetPregnancySummary},
	}
}

func (r womensHealthRegistrar) RegisterTools(registry *mcpserver.Registry) error {
	for _, entry := range womensHealthRegistrations() {
		if err := entry.register(registry, r.svc); err != nil {
			return err
		}
	}
	return nil
}

// newWomensHealthHarness drives the three tools over an MCP session against a
// scripted fake Garmin service, exactly as newToolHarness drives the registered
// surface.
func newWomensHealthHarness(t *testing.T, script testkit.Script) toolHarness {
	t.Helper()
	return newWomensHealthHarnessWith(t, script, client.Limits{})
}

func newWomensHealthHarnessWith(
	t *testing.T, script testkit.Script, limits client.Limits,
) toolHarness {
	t.Helper()

	fake := testkit.NewServer(t, script)
	rc, err := client.New(client.Config{
		Hosts:   fake.Hosts(protocol.DomainGlobal),
		Limits:  limits,
		Sleeper: client.SleeperFunc(func(context.Context, time.Duration) error { return nil }),
		Jitter:  func() float64 { return 0 },
	})
	if err != nil {
		t.Fatalf("client.New() = %v", err)
	}
	svc, err := newService(Deps{Client: rc, Caller: harnessCaller{doer: fake.Doer()}})
	if err != nil {
		t.Fatalf("newService() = %v", err)
	}
	return toolHarness{fake: fake, session: connectHarness(t, newWomensHealthServer(t, svc))}
}

// newWomensHealthServer builds a server carrying only the women's-health tools.
func newWomensHealthServer(t *testing.T, svc *service) *mcpserver.Server {
	t.Helper()

	return newDomainToolServer(t, "garmin-mcp-womenshealth-test",
		womensHealthRegistrations(), womensHealthRegistrar{svc: svc})
}

// womensHealthManifestEntry is the part of a manifest record these tests compare
// against.
type womensHealthManifestEntry struct {
	Name        string         `json:"name"`
	InputSchema map[string]any `json:"inputSchema"`
	Sensitivity string         `json:"sensitivity"`
	Effect      string         `json:"effect"`
	Idempotency string         `json:"idempotency"`
}

func loadWomensHealthManifest(t *testing.T) map[string]womensHealthManifestEntry {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("..", "..", "compat", "tools.json"))
	if err != nil {
		t.Fatalf("reading the manifest: %v", err)
	}
	var manifest struct {
		Tools []womensHealthManifestEntry `json:"tools"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("decoding the manifest: %v", err)
	}

	entries := make(map[string]womensHealthManifestEntry, len(manifest.Tools))
	for _, entry := range manifest.Tools {
		entries[entry.Name] = entry
	}
	return entries
}

// womensHealthPropertyNames renders the sorted property names of a schema.
func womensHealthPropertyNames(schema map[string]any) []string {
	raw, _ := schema["properties"].(map[string]any)
	names := make([]string, 0, len(raw))
	for name := range raw {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// womensHealthRequired renders the sorted required-argument names of a manifest
// schema.
func womensHealthRequired(schema map[string]any) []string {
	var out []string
	switch required := schema["required"].(type) {
	case []any:
		for _, value := range required {
			if name, ok := value.(string); ok {
				out = append(out, name)
			}
		}
	case []string:
		out = append(out, required...)
	}
	slices.Sort(out)
	return out
}

// assertWomensHealthContract checks one contract against its manifest record. The
// declared schema may be stricter — a format, a pattern or a range the manifest
// does not state — but it may not rename, add, drop or retype an argument, or
// change what is required.
func assertWomensHealthContract(t *testing.T, contract Contract, manifest womensHealthManifestEntry) {
	t.Helper()

	declared := contract.Schema.JSON()
	got := womensHealthPropertyNames(declared)
	want := womensHealthPropertyNames(manifest.InputSchema)
	if !slices.Equal(got, want) {
		t.Errorf("input properties drifted: declared %v, manifest %v", got, want)
	}
	if got, want := contract.Schema.Required(), womensHealthRequired(manifest.InputSchema); !slices.Equal(got, want) {
		t.Errorf("required arguments drifted: declared %v, manifest %v", got, want)
	}
	if additional, ok := declared[keyAdditionalProperties].(bool); !ok || additional {
		t.Error("the declared schema accepts properties it does not declare")
	}

	if got := contract.Spec.Category; got != manifest.Sensitivity {
		t.Errorf("log category = %q, manifest sensitivity = %q", got, manifest.Sensitivity)
	}
	if manifest.Effect != policy.TierReadOnly.String() || contract.Spec.Tier != policy.TierReadOnly {
		t.Errorf("tier = %v, manifest effect = %q; both must be read-only",
			contract.Spec.Tier, manifest.Effect)
	}
	annotations := contract.Spec.Annotations
	if !annotations.ReadOnly || annotations.Destructive || !annotations.Idempotent || !annotations.OpenWorld {
		t.Errorf("annotations = %+v, want read-only, non-destructive, idempotent, open-world", annotations)
	}
}

// TestWomensHealthContractsMatchTheManifest pins the three contracts against
// compat/tools.json now, before register.go lists them.
//
// The package contract test (contract_test.go) covers only what Contracts()
// returns, and that is built from register.go. Until these three are wired
// there, this is what keeps their names, argument sets, tier, hints and log
// category honest.
func TestWomensHealthContractsMatchTheManifest(t *testing.T) {
	t.Parallel()

	entries := loadWomensHealthManifest(t)
	for _, entry := range womensHealthRegistrations() {
		contract := entry.contract()
		t.Run(contract.Spec.Name, func(t *testing.T) {
			t.Parallel()

			manifest, ok := entries[contract.Spec.Name]
			if !ok {
				t.Fatalf("%s is not in the manifest", contract.Spec.Name)
			}
			assertWomensHealthContract(t, contract, manifest)
		})
	}
}

// renderLogValue renders one record the way redaction_test.go's logValue does,
// dropping the timestamp so a short numeric needle cannot collide with it.
func renderLogValue(value slog.LogValuer) string {
	var buffer bytes.Buffer
	options := &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
			if attr.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return attr
		},
	}
	slog.New(slog.NewTextHandler(&buffer, options)).Info("probe", slog.Any("result", value))
	return buffer.String()
}

// assertToolResultNotLoggable proves that handing a women's-health tool result to
// slog reports its shape only, never a needle drawn from its own content.
func assertToolResultNotLoggable(t *testing.T, value slog.LogValuer, needles ...string) {
	t.Helper()

	rendered := renderLogValue(value)
	for _, needle := range needles {
		if strings.Contains(rendered, needle) {
			t.Errorf("logging the result leaks %q: %s", needle, rendered)
		}
	}
	if !strings.Contains(rendered, "model=") {
		t.Errorf("the log record names no model shape: %s", rendered)
	}
}

func menstrualDayviewToolPath() string {
	return client.PathMenstrualDayviewPrefix + "/" + womensHealthTestDate
}

func TestGetMenstrualDataForDateSanitizesTheDocument(t *testing.T) {
	t.Parallel()

	fixture := `{"cycleDay":14,"phase":"LUTEAL","userProfilePK":900001}`
	script := testkit.NewScript().With(menstrualDayviewToolPath(), testkit.JSON(http.StatusOK, fixture))
	h := newWomensHealthHarness(t, script)

	result := h.call(t, ToolGetMenstrualDataForDate, map[string]any{argDate: womensHealthTestDate})
	if got, want := result["date"], womensHealthTestDate; got != want {
		t.Errorf("date = %v, want %v", got, want)
	}
	if got, ok := result["has_data"].(bool); !ok || !got {
		t.Errorf("has_data = %v, want true", result["has_data"])
	}

	document, ok := result["document"].(map[string]any)
	if !ok {
		t.Fatalf("document = %#v, want a structured object", result["document"])
	}
	if _, present := document["userProfilePK"]; present {
		t.Errorf("document %v still carries the identifying key userProfilePK", document)
	}
	if document["phase"] != "LUTEAL" {
		t.Errorf("document = %v, want phase LUTEAL preserved", document)
	}
	if got := number(t, result, "dropped_fields"); got != 1 {
		t.Errorf("dropped_fields = %v, want 1", got)
	}
}

func TestGetMenstrualDataForDateReportsNoDataForAnEmptyBody(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().
		With(menstrualDayviewToolPath(), testkit.Behavior{Status: http.StatusNoContent})
	h := newWomensHealthHarness(t, script)

	result := h.call(t, ToolGetMenstrualDataForDate, map[string]any{argDate: womensHealthTestDate})
	if got, ok := result["has_data"].(bool); ok && got {
		t.Errorf("has_data = true for an empty body, want false")
	}
	if _, present := result["document"]; present {
		t.Errorf("document = %v, want absent for an empty body", result["document"])
	}
}

func TestGetMenstrualDataForDateRefusesAMalformedDate(t *testing.T) {
	t.Parallel()

	h := newWomensHealthHarness(t, testkit.NewScript())

	advice := h.callError(t, ToolGetMenstrualDataForDate, map[string]any{argDate: malformedDate})
	assertNoRawPayload(t, advice)
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("requests = %d, want 0: a refused date costs no Garmin call", got)
	}
}

func TestGetMenstrualDataForDateResultIsNotLoggable(t *testing.T) {
	t.Parallel()

	result := MenstrualDay{
		Date:    womensHealthTestDate,
		HasData: true,
		Document: map[string]any{
			"phase": "LUTEAL_PHASE_MARKER",
			"note":  "cramping and fatigue",
		},
	}
	assertToolResultNotLoggable(t, result, "LUTEAL_PHASE_MARKER", "cramping and fatigue")
}

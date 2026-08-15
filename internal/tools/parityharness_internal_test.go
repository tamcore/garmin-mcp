package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// The activity-management parity tools are registered here rather than through
// register.go, because the composition of the real tier lists is owned elsewhere.
// The list below is the same one register.go takes, so a drifting contract name
// fails these tests before it can reach the wiring.
func parityRegistrations() []registration {
	return []registration{
		{countActivitiesContract, registerCountActivities},
		{getActivitiesForDateContract, registerGetActivitiesForDate},
		{getActivityContract, registerGetActivity},
		{getActivityGearContract, registerGetActivityGear},
		{getActivityTypesContract, registerGetActivityTypes},
	}
}

// parityRegistrar registers only the parity tools under test.
type parityRegistrar struct {
	svc *service
}

func (r parityRegistrar) RegisterTools(registry *mcpserver.Registry) error {
	for _, entry := range parityRegistrations() {
		if err := entry.register(registry, r.svc); err != nil {
			return err
		}
	}
	return nil
}

// Synthetic identities. No fixture in this file is a recording of a real account.
const (
	parityPrincipal  = "principal-parity-0001"
	parityActivityID = "987654321"
	parityDate       = "2026-01-31"
	parityVersion    = "0.0.0-parity"
)

// Argument and result keys the parity tests assert on, named once so a rename shows
// up in one place.
const (
	argActivityID  = "activity_id"
	argDate        = "date"
	keyDisplayName = "display_name"
	typeKeyRunning = "running"
)

// parityCaller is the principal-scoped caller for the fake service. In production
// this is *auth.Refresher; here testkit's Doer enforces the origin, so no credential
// is in play and no test can reach the real Garmin service.
type parityCaller struct {
	doer testkit.Doer
}

func (c parityCaller) Do(
	ctx context.Context, principal string, req *http.Request,
) (*http.Response, error) {
	if principal == "" {
		return nil, errors.New("parityCaller: no principal")
	}
	return c.doer.Do(req.WithContext(ctx))
}

// parityHarness drives the parity tools the way a real client does: over an MCP
// session, through the middleware chain, against a scripted fake Garmin service.
type parityHarness struct {
	fake    *testkit.Server
	session *mcp.ClientSession
}

func newParityHarness(t *testing.T, script testkit.Script) parityHarness {
	t.Helper()

	fake := testkit.NewServer(t, script)
	server := newParityServer(t, fake)

	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.MCPServer().Run(ctx, serverTransport)
	}()

	session, err := mcp.NewClient(&mcp.Implementation{
		Name: "garmin-mcp-parity-test", Version: "test",
	}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		cancel()
		t.Fatalf("connecting the test client: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Close()
		cancel()
		<-done
	})

	return parityHarness{fake: fake, session: session}
}

func newParityServer(t *testing.T, fake *testkit.Server) *mcpserver.Server {
	t.Helper()

	rc, err := client.New(client.Config{
		Hosts:   fake.Hosts(protocol.DomainGlobal),
		Sleeper: client.SleeperFunc(func(context.Context, time.Duration) error { return nil }),
		Jitter:  func() float64 { return 0 },
	})
	if err != nil {
		t.Fatalf("client.New() = %v", err)
	}
	svc, err := newService(Deps{Client: rc, Caller: parityCaller{doer: fake.Doer()}})
	if err != nil {
		t.Fatalf("newService() = %v", err)
	}

	resolver, err := identity.NewStdioResolver(identity.StdioConfig{
		PrincipalIDs: []string{parityPrincipal},
	})
	if err != nil {
		t.Fatalf("identity.NewStdioResolver() = %v", err)
	}
	pol, err := policy.New(policy.Config{
		Mode:          policy.ModeLocal,
		ReadOnlyTools: parityToolNames(),
	}, nil)
	if err != nil {
		t.Fatalf("policy.New() = %v", err)
	}

	server, err := mcpserver.New(mcpserver.Deps{
		Info:       mcpserver.Info{Name: "garmin-mcp-parity-test", Version: parityVersion},
		Policy:     pol,
		Principals: resolver,
		Registrars: []mcpserver.ToolRegistrar{parityRegistrar{svc: svc}},
	})
	if err != nil {
		t.Fatalf("mcpserver.New() = %v", err)
	}
	return server
}

// parityToolNames is the read-only tier the test server runs with: the parity tools
// plus the server's own built-in tool.
func parityToolNames() []string {
	return append([]string{mcpserver.ServerInfoToolName}, namesOf(parityRegistrations())...)
}

// call invokes a tool and requires it to succeed.
func (h parityHarness) call(t *testing.T, name string, args map[string]any) map[string]any {
	t.Helper()

	result := h.rawCall(t, name, args)
	if result.IsError {
		t.Fatalf("%s returned an error result: %s", name, parityText(result))
	}
	if result.StructuredContent == nil {
		t.Fatalf("%s returned no structured content", name)
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshalling %s structured content: %v", name, err)
	}
	var out map[string]any
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatalf("decoding %s structured content: %v", name, err)
	}
	return out
}

// callError invokes a tool and requires an error result, returning its text.
func (h parityHarness) callError(t *testing.T, name string, args map[string]any) string {
	t.Helper()

	result := h.rawCall(t, name, args)
	if !result.IsError {
		t.Fatalf("%s succeeded, want an error result", name)
	}
	return parityText(result)
}

func (h parityHarness) rawCall(
	t *testing.T, name string, args map[string]any,
) *mcp.CallToolResult {
	t.Helper()

	if args == nil {
		args = map[string]any{}
	}
	result, err := h.session.CallTool(t.Context(),
		&mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%q) transport error = %v", name, err)
	}
	return result
}

// text renders the whole result, structured content included, so a leak test can
// assert over everything that left the process.
func (h parityHarness) text(t *testing.T, name string, args map[string]any) string {
	t.Helper()

	result := h.rawCall(t, name, args)
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshalling the %s result: %v", name, err)
	}
	return string(encoded)
}

func parityText(result *mcp.CallToolResult) string {
	var text strings.Builder
	for _, content := range result.Content {
		if textContent, ok := content.(*mcp.TextContent); ok {
			text.WriteString(textContent.Text)
		}
	}
	return text.String()
}

// number reads a numeric field out of a structured result.
func number(t *testing.T, result map[string]any, key string) float64 {
	t.Helper()

	value, ok := result[key].(float64)
	if !ok {
		t.Fatalf("result[%q] = %#v, want a number", key, result[key])
	}
	return value
}

// object reads a nested object out of a structured result.
func object(t *testing.T, result map[string]any, key string) map[string]any {
	t.Helper()

	value, ok := result[key].(map[string]any)
	if !ok {
		t.Fatalf("result[%q] = %#v, want an object", key, result[key])
	}
	return value
}

// list reads an array out of a structured result.
func list(t *testing.T, result map[string]any, key string) []any {
	t.Helper()

	value, ok := result[key].([]any)
	if !ok {
		t.Fatalf("result[%q] = %#v, want an array", key, result[key])
	}
	return value
}

// entry reads one object element of an array.
func entry(t *testing.T, items []any, index int) map[string]any {
	t.Helper()

	value, ok := items[index].(map[string]any)
	if !ok {
		t.Fatalf("items[%d] = %#v, want an object", index, items[index])
	}
	return value
}

func parityActivityPath() string {
	return client.PathActivityPrefix + "/" + parityActivityID
}

func parityForDatePath() string {
	return client.PathActivitiesForDatePrefix + "/" + parityDate
}

// TestParityToolsDeclareTheReadOnlyContract covers all five at once: the upstream
// name, the read-only tier, the manifest's sensitivity category, and all four
// annotation hints. A wrong hint is not cosmetic — a client decides whether to
// prompt its user from it.
func TestParityToolsDeclareTheReadOnlyContract(t *testing.T) {
	t.Parallel()

	wantCategory := map[string]string{
		ToolCountActivities:      categoryOrdinary,
		ToolGetActivitiesForDate: categoryLocation,
		ToolGetActivity:          categoryLocation,
		ToolGetActivityGear:      categoryDevice,
		ToolGetActivityTypes:     categoryOrdinary,
	}
	for _, entry := range parityRegistrations() {
		spec := entry.contract().Spec
		t.Run(spec.Name, func(t *testing.T) {
			t.Parallel()

			if spec.Tier != policy.TierReadOnly {
				t.Errorf("tier = %v, want the read-only tier", spec.Tier)
			}
			if spec.Category != wantCategory[spec.Name] {
				t.Errorf("category = %q, want %q", spec.Category, wantCategory[spec.Name])
			}
			hints := spec.Annotations
			if !hints.ReadOnly || hints.Destructive || !hints.Idempotent || !hints.OpenWorld {
				t.Errorf("annotations = %+v, want read-only, idempotent, open-world", hints)
			}
			if spec.Description == "" || spec.Title == "" {
				t.Error("the tool declares no title or no description")
			}
		})
	}
}

// TestParityToolsAcceptNoAccountSelector is the principal rule, asserted over the
// whole slice: the account comes from the request context and from nowhere else.
func TestParityToolsAcceptNoAccountSelector(t *testing.T) {
	t.Parallel()

	forbidden := []string{"user_id", "email", keyDisplayName, "token", "token_path", "path"}
	for _, entry := range parityRegistrations() {
		contract := entry.contract()
		for _, property := range contract.Schema.Properties() {
			for _, name := range forbidden {
				if property.Name == name {
					t.Errorf("%s declares the argument %q", contract.Spec.Name, name)
				}
			}
		}
	}
}

// TestParityResultsLogTheirShapeAndNotTheirContent is the log-redaction test. A
// result that reaches a log sink by accident must reveal counts and presence flags
// only, never a measurement, a name or an identifier.
func TestParityResultsLogTheirShapeAndNotTheirContent(t *testing.T) {
	t.Parallel()

	name := "Synthetic run"
	measurement := 987654321.0
	values := []slog.LogValuer{
		ActivityCount{TotalActivities: 4242, Note: countActivitiesNote},
		ActivityDetail{ActivityID: 987654321, Name: &name, ActivityType: &name},
		ActivityGearList{ActivityID: 987654321, Gear: []ActivityGear{{DisplayName: &name}}, Count: 1},
		ActivityTypeList{ActivityTypes: []ActivityTypeEntry{{TypeKey: &name}}, Count: 1},
		DailyActivityList{
			Date:       parityDate,
			Activities: []DailyActivity{{Name: &name, DistanceMeters: &measurement}},
			Count:      1,
		},
	}
	for _, value := range values {
		rendered := fmt.Sprintf("%v", value.LogValue())
		for _, forbidden := range []string{name, "987654321", "4242", parityDate} {
			if strings.Contains(rendered, forbidden) {
				t.Errorf("%T logged %q, which its LogValue must withhold", value, forbidden)
			}
		}
		if !strings.Contains(rendered, "model=") {
			t.Errorf("%T logged %q, which names no model", value, rendered)
		}
	}
}

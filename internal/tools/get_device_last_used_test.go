package tools

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// This file also carries the shared test harness for all six device and gear tools
// this slice adds (get_device_last_used, get_device_settings,
// get_primary_training_device, get_device_solar_data, get_device_alarms and
// get_gear): a standalone registrar, server and session, so each tool can be driven
// end to end before register.go carries it. It follows the same pattern
// garmincoach_internal_test.go established for a tool ahead of its own wiring.
//
// Every identifier and name in the fixtures below is synthetic. No fixture in this
// package is a recording of a real account.
const deviceToolsPrincipal = "principal-device-tools-0001"

// deviceToolsCaller dispatches for one principal against the fake service. No
// credential is in play: the fake's Doer enforces the origin, so no test can reach
// real Garmin.
type deviceToolsCaller struct {
	doer testkit.Doer
}

func (c deviceToolsCaller) Do(
	ctx context.Context, principal string, req *http.Request,
) (*http.Response, error) {
	if principal == "" {
		return nil, errors.New("deviceToolsCaller: no principal")
	}
	return c.doer.Do(req.WithContext(ctx))
}

// deviceToolsRegistrar registers exactly the six tools this slice adds.
type deviceToolsRegistrar struct {
	svc *service
}

func (r deviceToolsRegistrar) RegisterTools(registry *mcpserver.Registry) error {
	for _, register := range []func(*mcpserver.Registry, *service) error{
		registerGetDeviceLastUsed,
		registerGetDeviceSettings,
		registerGetPrimaryTrainingDevice,
		registerGetDeviceSolarData,
		registerGetDeviceAlarms,
		registerGetGear,
	} {
		if err := register(registry, r.svc); err != nil {
			return err
		}
	}
	return nil
}

// deviceToolsService builds the tool service over a scripted fake Garmin service.
func deviceToolsService(t *testing.T, script testkit.Script) (*service, *testkit.Server) {
	t.Helper()

	fake := testkit.NewServer(t, script)
	rc, err := client.New(client.Config{Hosts: fake.Hosts(protocol.DomainGlobal)})
	if err != nil {
		t.Fatalf("client.New() = %v", err)
	}
	svc, err := newService(Deps{Client: rc, Caller: deviceToolsCaller{doer: fake.Doer()}})
	if err != nil {
		t.Fatalf("newService() = %v", err)
	}
	return svc, fake
}

// deviceToolsContext returns a request context carrying a resolved principal, which
// is the only way a tool can learn whose account it reads.
func deviceToolsContext(t *testing.T) context.Context {
	t.Helper()

	principal, err := identity.NewPrincipal(deviceToolsPrincipal)
	if err != nil {
		t.Fatalf("identity.NewPrincipal() = %v", err)
	}
	return identity.WithPrincipal(t.Context(), principal)
}

// deviceToolsServer stands up the real server carrying only the six tools this
// slice adds.
func deviceToolsServer(t *testing.T, svc *service) *mcpserver.Server {
	t.Helper()

	resolver, err := identity.NewStdioResolver(identity.StdioConfig{
		PrincipalIDs: []string{deviceToolsPrincipal},
	})
	if err != nil {
		t.Fatalf("identity.NewStdioResolver() = %v", err)
	}
	pol, err := policy.New(policy.Config{
		Mode: policy.ModeLocal,
		ReadOnlyTools: []string{
			mcpserver.ServerInfoToolName,
			ToolGetDeviceLastUsed, ToolGetDeviceSettings, ToolGetPrimaryTrainingDevice,
			ToolGetDeviceSolarData, ToolGetDeviceAlarms, ToolGetGear,
		},
	}, nil)
	if err != nil {
		t.Fatalf("policy.New() = %v", err)
	}

	server, err := mcpserver.New(mcpserver.Deps{
		Info:       mcpserver.Info{Name: "garmin-mcp-device-tools-test", Version: "0.0.0-device-tools-test"},
		Policy:     pol,
		Principals: resolver,
		Registrars: []mcpserver.ToolRegistrar{deviceToolsRegistrar{svc: svc}},
	})
	if err != nil {
		t.Fatalf("mcpserver.New() = %v", err)
	}
	return server
}

// deviceToolsSession connects an in-memory MCP client to the server above.
func deviceToolsSession(t *testing.T, server *mcpserver.Server) *mcp.ClientSession {
	t.Helper()

	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.MCPServer().Run(ctx, serverTransport)
	}()

	session, err := mcp.NewClient(&mcp.Implementation{
		Name: "garmin-mcp-device-tools-test", Version: "test",
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
	return session
}

// callDeviceTool invokes a registered tool over the session and returns the result.
func callDeviceTool(
	t *testing.T, session *mcp.ClientSession, name string, args map[string]any,
) *mcp.CallToolResult {
	t.Helper()

	if args == nil {
		args = map[string]any{}
	}
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%q) transport error = %v", name, err)
	}
	return result
}

// deviceToolStructured decodes a successful call's structured content.
func deviceToolStructured(t *testing.T, result *mcp.CallToolResult) map[string]any {
	t.Helper()

	if result.IsError {
		t.Fatalf("call returned an error result: %s", harnessText(result))
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshalling structured content: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatalf("decoding structured content: %v", err)
	}
	return out
}

// syntheticWatchName is the synthetic device display name shared by the
// last-used-device and primary-training-device fixtures below.
const syntheticWatchName = "Synthetic Watch"

// deviceLastUsedBody is a synthetic last-used-device document.
const deviceLastUsedBody = `{"userDeviceId":998877,"lastUsedDeviceName":"` + syntheticWatchName + `",` +
	`"lastUsedDeviceApplicationKey":"synthetic-app-key","userProfileNumber":135790,` +
	`"lastUsedDeviceUploadTime":1750000000000,"imageUrl":"https://example.invalid/device.png"}`

func TestGetDeviceLastUsedDeclaresTheUpstreamContract(t *testing.T) {
	t.Parallel()

	contract := getDeviceLastUsedContract()
	if got := contract.Spec.Name; got != ToolGetDeviceLastUsed {
		t.Errorf("wire name = %q, want %q", got, ToolGetDeviceLastUsed)
	}
	if got := contract.Spec.Tier; got != policy.TierReadOnly {
		t.Errorf("tier = %v, want the read-only tier", got)
	}
	if got := contract.Spec.Category; got != categoryDevice {
		t.Errorf("log category = %q, want %q", got, categoryDevice)
	}
	want := mcpserver.Annotations{ReadOnly: true, Destructive: false, Idempotent: true, OpenWorld: true}
	if got := contract.Spec.Annotations; got != want {
		t.Errorf("annotations = %+v, want %+v", got, want)
	}

	schema := contract.Schema
	if got := schema.Required(); len(got) != 0 {
		t.Errorf("required = %v, want none", got)
	}
	if len(schema.Properties()) != 0 {
		t.Errorf("declared %d properties, want none", len(schema.Properties()))
	}
}

func TestGetDeviceLastUsedRegistersInTheReadOnlyTier(t *testing.T) {
	t.Parallel()

	svc, _ := deviceToolsService(t, testkit.NewScript())
	server := deviceToolsServer(t, svc)

	spec, ok := server.Registry().Spec(ToolGetDeviceLastUsed)
	if !ok {
		t.Fatal("the registry recorded no spec for get_device_last_used")
	}
	if spec.Tier != policy.TierReadOnly {
		t.Errorf("registered tier = %v, want the read-only tier", spec.Tier)
	}
}

func TestGetDeviceLastUsedReturnsTheCuratedDocument(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathDeviceLastUsed,
		testkit.JSON(http.StatusOK, deviceLastUsedBody))
	svc, fake := deviceToolsService(t, script)

	last, err := svc.devices.LastUsed(deviceToolsContext(t), mustSession(t, svc, deviceToolsPrincipal))
	if err != nil {
		t.Fatalf("LastUsed() = %v", err)
	}
	result := newDeviceLastUsed(last)

	if result.UserDeviceID == nil || *result.UserDeviceID != 998877 {
		t.Errorf("user_device_id = %v, want 998877", result.UserDeviceID)
	}
	if result.DeviceName == nil || *result.DeviceName != syntheticWatchName {
		t.Errorf("device_name = %v, want %q", result.DeviceName, syntheticWatchName)
	}
	if result.DeviceKey == nil || *result.DeviceKey != "synthetic-app-key" {
		t.Errorf("device_key = %v", result.DeviceKey)
	}
	if result.UserProfileID == nil || *result.UserProfileID != 135790 {
		t.Errorf("user_profile_id = %v, want 135790", result.UserProfileID)
	}
	if result.LastUploadTimeMillis == nil || *result.LastUploadTimeMillis != 1750000000000 {
		t.Errorf("last_upload_time_millis = %v, want 1750000000000", result.LastUploadTimeMillis)
	}
	if got := len(fake.Requests()); got != 1 {
		t.Errorf("dispatched %d requests, want 1", got)
	}
}

func TestGetDeviceLastUsedAnswersOverTheWire(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathDeviceLastUsed,
		testkit.JSON(http.StatusOK, deviceLastUsedBody))
	svc, _ := deviceToolsService(t, script)
	session := deviceToolsSession(t, deviceToolsServer(t, svc))

	result := callDeviceTool(t, session, ToolGetDeviceLastUsed, nil)
	out := deviceToolStructured(t, result)
	if got := out["device_name"]; got != syntheticWatchName {
		t.Errorf("device_name = %v, want %q", got, syntheticWatchName)
	}
}

func TestGetDeviceLastUsedRefusesARegistryItDoesNotHave(t *testing.T) {
	t.Parallel()

	svc, _ := deviceToolsService(t, testkit.NewScript())
	err := registerGetDeviceLastUsed(nil, svc)
	if !errors.Is(err, mcpserver.ErrMissingDependency) {
		t.Errorf("registering on no registry = %v, want ErrMissingDependency", err)
	}
}

// mustSession builds a session for the given principal, over the service's caller.
func mustSession(t *testing.T, svc *service, principal string) client.Session {
	t.Helper()

	session, err := client.NewSession(svc.caller, principal)
	if err != nil {
		t.Fatalf("client.NewSession() = %v", err)
	}
	return session
}

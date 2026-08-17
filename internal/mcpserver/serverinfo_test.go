package mcpserver_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
)

// callServerInfo drives server_info end to end and decodes its structured result.
func callServerInfo(t *testing.T, server *mcpserver.Server) mcpserver.ServerInfo {
	t.Helper()

	ctx := context.Background()
	session := connectClient(t, ctx, server, nil)
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: mcpserver.ServerInfoToolName})
	if err != nil {
		t.Fatalf("CallTool returned error: %v", err)
	}
	if result.IsError {
		t.Fatalf("server_info returned an error result: %v", result.Content)
	}

	var info mcpserver.ServerInfo
	structured, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshaling structured content returned error: %v", err)
	}
	if err := json.Unmarshal(structured, &info); err != nil {
		t.Fatalf("unmarshaling %s returned error: %v", structured, err)
	}
	return info
}

// TestServerInfoReportsOnlyTheReadOnlyTierOnStdio proves the effective tiers
// track what the caller can actually reach, not what the operator's tier lists
// merely name.
func TestServerInfoReportsOnlyTheReadOnlyTierOnStdio(t *testing.T) {
	t.Parallel()

	server, _, _ := tieredServer(t, nil)
	info := callServerInfo(t, server)

	if !slices.Equal(info.EnabledTiers, []string{tierReadOnly}) {
		t.Fatalf("EnabledTiers = %v, want only [read-only]", info.EnabledTiers)
	}
	if len(info.GrantedScopes) != 0 {
		t.Fatalf("GrantedScopes = %v, want none on stdio", info.GrantedScopes)
	}
}

// TestServerInfoReportsEveryEnabledAndGrantedTier proves both tiers show up once
// both are enabled and granted.
func TestServerInfoReportsEveryEnabledAndGrantedTier(t *testing.T) {
	t.Parallel()

	server, _, _ := tieredServer(t, destructiveEnabled(t))
	info := callServerInfo(t, server)

	want := []string{tierReadOnly, tierWrite, tierDestructive}
	slices.Sort(want)
	got := slices.Clone(info.EnabledTiers)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("EnabledTiers = %v, want %v", info.EnabledTiers, want)
	}

	wantScopes := []string{"garmin:write", "garmin:destructive"}
	slices.Sort(wantScopes)
	gotScopes := slices.Clone(info.GrantedScopes)
	slices.Sort(gotScopes)
	if !slices.Equal(gotScopes, wantScopes) {
		t.Fatalf("GrantedScopes = %v, want %v", info.GrantedScopes, wantScopes)
	}
}

// TestServerInfoOmitsTheDestructiveTierWhenOnlyWriteIsGranted covers destructive
// enablement additionally requiring a granted destructive scope, not merely
// write enablement.
func TestServerInfoOmitsTheDestructiveTierWhenOnlyWriteIsGranted(t *testing.T) {
	t.Parallel()

	server, _, _ := tieredServer(t, writeOnlyGranted(t))
	info := callServerInfo(t, server)

	want := []string{tierReadOnly, tierWrite}
	slices.Sort(want)
	got := slices.Clone(info.EnabledTiers)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("EnabledTiers = %v, want %v", info.EnabledTiers, want)
	}
}

// TestServerInfoVisibleToolCountMatchesToolsList proves VisibleToolCount is not
// a second, independently drifting number: it is exactly the size of what
// tools/list itself returns for the same caller.
func TestServerInfoVisibleToolCountMatchesToolsList(t *testing.T) {
	t.Parallel()

	server, _, _ := tieredServer(t, writeOnlyGranted(t))
	ctx := context.Background()
	session := connectClient(t, ctx, server, nil)

	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools returned error: %v", err)
	}
	info := callServerInfo(t, server)

	if info.VisibleToolCount != len(listed.Tools) {
		t.Fatalf("VisibleToolCount = %d, want %d (len of tools/list)",
			info.VisibleToolCount, len(listed.Tools))
	}
	if info.ToolCount != server.Registry().Len() {
		t.Fatalf("ToolCount = %d, want the registered total %d", info.ToolCount, server.Registry().Len())
	}
}

// TestServerInfoDisclosesNoAccountDataWithScopesReported extends the existing
// no-disclosure test to the new fields: naming tiers and scopes must not mean
// naming the principal.
func TestServerInfoDisclosesNoAccountDataWithScopesReported(t *testing.T) {
	t.Parallel()

	server, _, _ := tieredServer(t, destructiveEnabled(t))
	ctx := context.Background()
	session := connectClient(t, ctx, server, nil)

	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: mcpserver.ServerInfoToolName})
	if err != nil {
		t.Fatalf("CallTool returned error: %v", err)
	}

	rendered, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshaling the result returned error: %v", err)
	}
	if strings.Contains(string(rendered), testPrincipalID) {
		t.Fatalf("server_info result %s discloses the principal identifier", rendered)
	}
}

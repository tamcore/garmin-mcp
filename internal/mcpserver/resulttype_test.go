package mcpserver_test

import (
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The SDK's own client tolerates a missing resultType, so only the wire shows it.
// A client on 2026-07-28 rejects a tools/call result without one.
func TestEveryToolCallResultCarriesResultTypeOnTheNewProtocol(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		tool         string
		capabilities map[string]any
		want         string
	}{
		{"confirmation request", destructiveTool, map[string]any{"elicitation": map[string]any{}}, "input_required"},
		{"refusal built by middleware", destructiveTool, map[string]any{}, "complete"},
		{"result built by the handler", writeTool, map[string]any{}, "complete"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			server, _, _ := tieredServer(t, destructiveEnabled(t))
			result := rawNewProtocolCall(t, server.MCPServer(), tc.tool, tc.capabilities)

			var got struct {
				ResultType string `json:"resultType"`
			}
			if err := json.Unmarshal(result, &got); err != nil {
				t.Fatalf("decoding result: %v", err)
			}
			if got.ResultType != tc.want {
				t.Fatalf("resultType = %q, want %q in %s", got.ResultType, tc.want, result)
			}
		})
	}
}

func rawNewProtocolCall(t *testing.T, server *mcp.Server, tool string, capabilities map[string]any) json.RawMessage {
	t.Helper()
	ctx := t.Context()

	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	session, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server Connect returned error: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	conn, err := clientTransport.Connect(ctx)
	if err != nil {
		t.Fatalf("client Connect returned error: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	params, err := json.Marshal(map[string]any{
		"name":      tool,
		"arguments": map[string]any{textArg: testText},
		"_meta": map[string]any{
			mcp.MetaKeyProtocolVersion:    "2026-07-28",
			mcp.MetaKeyClientCapabilities: capabilities,
		},
	})
	if err != nil {
		t.Fatalf("encoding params: %v", err)
	}
	id, _ := jsonrpc.MakeID(float64(1))
	if err := conn.Write(ctx, &jsonrpc.Request{ID: id, Method: "tools/call", Params: params}); err != nil {
		t.Fatalf("writing request: %v", err)
	}
	msg, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("reading response: %v", err)
	}
	resp, ok := msg.(*jsonrpc.Response)
	if !ok || resp.Error != nil {
		t.Fatalf("want a result, got %#v", msg)
	}
	return resp.Result
}

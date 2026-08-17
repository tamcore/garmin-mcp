package mcpserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/mcplog"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// Synthetic names and values shared by these tests.
const (
	testServerName  = "garmin-mcp-test"
	testVersion     = "0.0.0-test"
	textArg         = "text"
	testPrincipalID = "principal-a"
	testCategory    = "diagnostics"
	testText        = "hello"
	msgToolCall     = "tool call"
	echoTool        = "echo"
	readTool        = "read_thing"
	writeTool       = "write_thing"
	destructiveTool = "delete_thing"

	// tierReadOnly, tierWrite and tierDestructive are policy.Tier's String() form,
	// shared so tests do not repeat the literal.
	tierReadOnly    = "read-only"
	tierWrite       = "write"
	tierDestructive = "destructive"
)

// syncBuffer is a concurrency-safe log sink. The server logs from the request
// goroutine while the test reads from its own, so the buffer needs a lock.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Records decodes every JSON log line emitted so far.
func (b *syncBuffer) Records(t *testing.T) []map[string]any {
	t.Helper()

	var records []map[string]any
	for line := range strings.SplitSeq(strings.TrimSpace(b.String()), "\n") {
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("log line %q is not JSON: %v", line, err)
		}
		records = append(records, record)
	}
	return records
}

func mustPolicy(t *testing.T, cfg policy.Config) *policy.Policy {
	t.Helper()

	p, err := policy.New(cfg, policy.NoScopes{})
	if err != nil {
		t.Fatalf("policy.New returned error: %v", err)
	}
	return p
}

func mustResolver(t *testing.T) identity.Resolver {
	t.Helper()

	resolver, err := identity.NewStdioResolver(
		identity.StdioConfig{PrincipalIDs: []string{testPrincipalID}})
	if err != nil {
		t.Fatalf("NewStdioResolver returned error: %v", err)
	}
	return resolver
}

func mustLogger(t *testing.T, sink *syncBuffer, cfg mcplog.Config) *mcplog.Logger {
	t.Helper()

	logger, err := mcplog.New(sink, cfg)
	if err != nil {
		t.Fatalf("mcplog.New returned error: %v", err)
	}
	return logger
}

// testDeps builds a minimal, fully injected dependency set. The policy names only
// the built-in tool, so a test that registers more must widen it.
func testDeps(t *testing.T, registrars ...mcpserver.ToolRegistrar) mcpserver.Deps {
	t.Helper()

	return mcpserver.Deps{
		Info:   mcpserver.Info{Name: testServerName, Version: testVersion},
		Logger: mustLogger(t, &syncBuffer{}, mcplog.Config{}),
		Policy: mustPolicy(t, policy.Config{
			Mode:          policy.ModeLocal,
			ReadOnlyTools: []string{mcpserver.ServerInfoToolName},
		}),
		Principals: mustResolver(t),
		Registrars: registrars,
	}
}

func newTestServer(t *testing.T, deps mcpserver.Deps) *mcpserver.Server {
	t.Helper()

	server, err := mcpserver.New(deps)
	if err != nil {
		t.Fatalf("mcpserver.New returned error: %v", err)
	}
	return server
}

// connectClient wires an in-memory client session to the server and cleans it up.
func connectClient(t *testing.T, ctx context.Context, server *mcpserver.Server,
	opts *mcp.ClientOptions,
) *mcp.ClientSession {
	t.Helper()

	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	serverSession, err := server.MCPServer().Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server Connect returned error: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, opts)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client Connect returned error: %v", err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })

	return clientSession
}

package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tamcore/garmin-mcp/internal/garmin/auth"
	"github.com/tamcore/garmin-mcp/internal/loginweb"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// The synthetic tools a fake registrar contributes.
const (
	fakeReadTool  = "fake_read_thing"
	fakeWriteTool = "fake_write_thing"
)

// fakeRegistrar stands in for the tool package. It records that it was called and
// registers one tool per tier, which is what the policy is then validated against.
type fakeRegistrar struct {
	calls int
	deps  ToolDeps
}

func (f *fakeRegistrar) RegisterTools(registry *mcpserver.Registry) error {
	f.calls++
	if err := addFakeTool(registry, fakeReadTool, policy.TierReadOnly); err != nil {
		return err
	}
	return addFakeTool(registry, fakeWriteTool, policy.TierWrite)
}

// fakeArgs is the empty input schema the synthetic tools take.
type fakeArgs struct{}

func addFakeTool(registry *mcpserver.Registry, name string, tier policy.Tier) error {
	return mcpserver.AddTool(registry, mcpserver.ToolSpec{
		Name:        name,
		Description: "a synthetic tool used to exercise the registration seam",
		Tier:        tier,
		Category:    "diagnostics",
		Annotations: mcpserver.Annotations{
			ReadOnly:   tier == policy.TierReadOnly,
			Idempotent: true,
			OpenWorld:  true,
		},
	}, func(context.Context, *mcp.CallToolRequest, fakeArgs) (
		*mcp.CallToolResult, any, error,
	) {
		return &mcp.CallToolResult{}, nil, nil
	})
}

// TestTheToolSeamAcceptsAFakeRegistry is the registration seam in one test: the
// composition root depends on a factory and the mcpserver interface, never on a tool
// package, so a fake registry wires up exactly like the real one.
func TestTheToolSeamAcceptsAFakeRegistry(t *testing.T) {
	registrar := &fakeRegistrar{}
	factory := func(deps ToolDeps) (ToolSet, error) {
		registrar.deps = deps
		return ToolSet{
			Registrar: registrar,
			ReadOnly:  []string{fakeReadTool},
			Write:     []string{fakeWriteTool},
		}, nil
	}

	deps, err := newDependencies(localConfig(t), &wiring{Tools: factory, Version: "v0.0.0-test"})
	if err != nil {
		t.Fatalf("newDependencies returned error: %v", err)
	}
	t.Cleanup(deps.close)

	if registrar.deps.Client == nil || registrar.deps.Caller == nil {
		t.Error("the factory received no request layer or no caller")
	}
	for name, present := range map[string]bool{
		"activities":       registrar.deps.Activities != nil,
		"activity details": registrar.deps.ActivityDetails != nil,
		"devices":          registrar.deps.Devices != nil,
		"profile":          registrar.deps.Profile != nil,
		"wellness":         registrar.deps.Wellness != nil,
	} {
		if !present {
			t.Errorf("the factory received no %s client", name)
		}
	}

	if tier, ok := deps.policy.TierOf(fakeReadTool); !ok || tier != policy.TierReadOnly {
		t.Errorf("the read-only tier list did not reach the policy: tier=%v ok=%v", tier, ok)
	}
	if tier, ok := deps.policy.TierOf(fakeWriteTool); !ok || tier != policy.TierWrite {
		t.Errorf("the write tier list did not reach the policy: tier=%v ok=%v", tier, ok)
	}

	server, err := mcpserver.New(deps.serverDeps())
	if err != nil {
		t.Fatalf("mcpserver.New returned error: %v", err)
	}
	if registrar.calls != 1 {
		t.Errorf("the registrar was called %d times, want 1", registrar.calls)
	}
	names := server.ToolNames()
	for _, want := range []string{mcpserver.ServerInfoToolName, fakeReadTool, fakeWriteTool} {
		if !slices.Contains(names, want) {
			t.Errorf("tool %q is not registered: %v", want, names)
		}
	}
}

// TestAFailingToolFactoryStopsStartUp keeps a broken tool package a start-up failure
// rather than a server that silently serves fewer tools.
func TestAFailingToolFactoryStopsStartUp(t *testing.T) {
	failure := errors.New("the tool package could not be built")
	factory := func(ToolDeps) (ToolSet, error) { return ToolSet{}, failure }

	_, err := newDependencies(localConfig(t), &wiring{Tools: factory})
	if !errors.Is(err, failure) {
		t.Errorf("error %v does not carry the factory's failure", err)
	}
}

// TestImportedTokensNeverReplaceAStoredRecord is the rule that keeps a rotated
// refresh token: a configured document seeds an empty store and is ignored
// afterwards, because the stored record may be newer than the file.
func TestImportedTokensNeverReplaceAStoredRecord(t *testing.T) {
	cfg := localConfig(t)
	cfg.GarminTokensPath = writeLegacyTokenFile(t, "synthetic-refresh-one")

	deps, err := newDependencies(cfg, nil)
	if err != nil {
		t.Fatalf("newDependencies returned error: %v", err)
	}
	t.Cleanup(deps.close)

	ctx := t.Context()
	if err := deps.importConfiguredTokens(ctx); err != nil {
		t.Fatalf("importConfiguredTokens returned error: %v", err)
	}
	stored, version, err := deps.files.Load(ctx, deps.principal.ID())
	if err != nil {
		t.Fatalf("the token set was not imported: %v", err)
	}
	if stored.RefreshToken() != "synthetic-refresh-one" {
		t.Error("the imported refresh token is not the one in the document")
	}

	// A second run with a different document must change nothing.
	deps.cfg.GarminTokensPath = writeLegacyTokenFile(t, "synthetic-refresh-two")
	if err := deps.importConfiguredTokens(ctx); err != nil {
		t.Fatalf("the second import returned error: %v", err)
	}
	after, afterVersion, err := deps.files.Load(ctx, deps.principal.ID())
	if err != nil {
		t.Fatalf("the stored record disappeared: %v", err)
	}
	if after.RefreshToken() != "synthetic-refresh-one" || afterVersion != version {
		t.Error("an import replaced a stored record, losing a possibly rotated refresh token")
	}
}

// writeLegacyTokenFile writes a synthetic 0.3.x token document and returns its path.
func writeLegacyTokenFile(t *testing.T, refreshToken string) string {
	t.Helper()

	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve the temporary directory: %v", err)
	}
	path := filepath.Join(dir, "garmin_tokens.json")
	document := `{"di_token":"synthetic-di-token","di_refresh_token":"` + refreshToken +
		`","di_client_id":"synthetic-client"}`
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatalf("write the token document: %v", err)
	}
	return path
}

// TestAttemptOfCarriesNothingForAZeroResult keeps the projection honest for the
// result a failed login produces.
func TestAttemptOfCarriesNothingForAZeroResult(t *testing.T) {
	attempt := attemptOf(auth.Result{})

	if attempt.NeedsMFA || attempt.TransactionID != "" || attempt.MFAMethod != "" {
		t.Errorf("a zero result produced %+v, want an empty attempt", attempt)
	}
}

// TestLaunchBrowserRefusesAnythingButThisRunsPage keeps the launcher from being a
// general-purpose URL opener.
func TestLaunchBrowserRefusesAnythingButThisRunsPage(t *testing.T) {
	for _, endpoint := range []string{
		"https://example.test/login",
		"http://192.0.2.10:8180/",
		"file:///etc/passwd",
		"",
	} {
		t.Run(endpoint, func(t *testing.T) {
			err := launchBrowser(t.Context(), endpoint)
			if !errors.Is(err, ErrNoBrowser) {
				t.Errorf("error %v does not match ErrNoBrowser", err)
			}
			if endpoint != "" && strings.Contains(err.Error(), endpoint) {
				t.Error("the refusal echoes the rejected URL")
			}
		})
	}
}

// TestIsGracefulStopTreatsACancelledRunAsClean keeps a supervisor's stop out of the
// failure exit codes.
func TestIsGracefulStopTreatsACancelledRunAsClean(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name string
		ctx  context.Context
		err  error
		want bool
	}{
		{name: "no error", ctx: context.Background(), want: true},
		{name: "cancelled error", ctx: context.Background(), err: context.Canceled, want: true},
		{
			name: "io failure after cancellation",
			ctx:  cancelled, err: errors.New("read: closed"), want: true,
		},
		{
			name: "genuine failure",
			ctx:  context.Background(), err: errors.New("read: closed"), want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isGracefulStop(tc.ctx, tc.err); got != tc.want {
				t.Errorf("isGracefulStop = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestCredentialHelpersBoundAndClear covers the two terminal-flow rules that hold
// whatever the platform: a value over the bound is refused without being echoed, and
// the variables are cleared once the login call returns.
func TestCredentialHelpersBoundAndClear(t *testing.T) {
	_, err := boundedValue(strings.Repeat("a", maxTerminalPasswordLen+1), maxTerminalPasswordLen)
	if !errors.Is(err, ErrCredentialTooLong) {
		t.Errorf("error %v does not match ErrCredentialTooLong", err)
	}
	if value, err := boundedValue("short", maxTerminalPasswordLen); err != nil || value != "short" {
		t.Errorf("boundedValue = %q, %v, want the value unchanged", value, err)
	}

	email, password := emailPrincipal, "synthetic-password"
	dropCredentials(&email, &password)
	if email != "" || password != "" {
		t.Error("dropCredentials left credential material behind")
	}
}

// TestReadLineBoundsWhatItReads proves the terminal reader stops at the bound rather
// than consuming an unbounded line.
func TestReadLineBoundsWhatItReads(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("build the pipe: %v", err)
	}
	t.Cleanup(func() { _ = reader.Close() })

	go func() {
		_, _ = writer.WriteString(strings.Repeat("a", 4*maxTerminalEmailLen) + "\n")
		_ = writer.Close()
	}()

	var prompt strings.Builder
	if _, err := readLine(reader, &prompt, "Email: ", maxTerminalEmailLen); !errors.Is(
		err, ErrCredentialTooLong) {
		t.Errorf("error %v does not match ErrCredentialTooLong", err)
	}
	if !strings.Contains(prompt.String(), "Email: ") {
		t.Errorf("the prompt was not written: %q", prompt.String())
	}
}

// TestDescribeChallengeNamesTheMethodWithoutEchoingAnything keeps the terminal
// challenge text free of anything the user typed.
func TestDescribeChallengeNamesTheMethodWithoutEchoingAnything(t *testing.T) {
	var out strings.Builder
	describeChallenge(&out, loginweb.Attempt{
		NeedsMFA:          true,
		MFAMethod:         "email",
		DeliveryUncertain: true,
	})

	rendered := out.String()
	if !strings.Contains(rendered, "email") {
		t.Errorf("the challenge text does not name the delivery method: %q", rendered)
	}
	if !strings.Contains(rendered, "could not be confirmed") {
		t.Errorf("the challenge text does not report uncertain delivery: %q", rendered)
	}
}

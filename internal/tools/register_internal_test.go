package tools

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/mcpserver"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// nopCaller never dispatches: start-up validation must fail before any Garmin call,
// so a caller that refuses to act is the honest stand-in.
type nopCaller struct{}

func (nopCaller) Do(context.Context, string, *http.Request) (*http.Response, error) {
	return nil, errors.New("nopCaller: start-up validation must not reach Garmin")
}

func testDeps(t *testing.T) Deps {
	t.Helper()

	hosts, err := protocol.NewHosts(protocol.DomainGlobal)
	if err != nil {
		t.Fatalf("protocol.NewHosts() = %v", err)
	}
	rc, err := client.New(client.Config{Hosts: hosts})
	if err != nil {
		t.Fatalf("client.New() = %v", err)
	}
	return Deps{Client: rc, Caller: nopCaller{}}
}

// startServer registers lists through the real server, which is where the tier lists
// are checked against the tools that actually got registered.
func startServer(t *testing.T, deps Deps, lists tierLists) error {
	t.Helper()

	registrar, err := newWithLists(deps, lists)
	if err != nil {
		return err
	}
	resolver, err := identity.NewStdioResolver(identity.StdioConfig{
		PrincipalIDs: []string{"principal-startup-0001"},
	})
	if err != nil {
		t.Fatalf("identity.NewStdioResolver() = %v", err)
	}
	pol, err := policy.New(policy.Config{
		Mode:             policy.ModeLocal,
		ReadOnlyTools:    lists.readOnly,
		WriteTools:       lists.write,
		DestructiveTools: lists.destructive,
	}, nil)
	if err != nil {
		t.Fatalf("policy.New() = %v", err)
	}

	_, err = mcpserver.New(mcpserver.Deps{
		Info:       mcpserver.Info{Name: "garmin-mcp-test", Version: "0.0.0-test"},
		Policy:     pol,
		Principals: resolver,
		Registrars: []mcpserver.ToolRegistrar{registrar},
	})
	return err
}

func TestStartupAcceptsTheRealTierLists(t *testing.T) {
	t.Parallel()

	if err := startServer(t, testDeps(t), defaultTierLists()); err != nil {
		t.Fatalf("start-up with the real tier lists = %v, want no error", err)
	}
}

func TestATypoInAWriteTierListFailsAtStartup(t *testing.T) {
	t.Parallel()

	lists := defaultTierLists()
	lists.write = []string{"add_weigh_inn"}

	err := startServer(t, testDeps(t), lists)
	if !errors.Is(err, ErrUnknownTierTool) {
		t.Fatalf("start-up with a typo in the write list = %v, want ErrUnknownTierTool", err)
	}
	if !strings.Contains(err.Error(), "add_weigh_inn") {
		t.Errorf("the failure %q does not name the offending entry", err)
	}
}

func TestATypoInADestructiveTierListFailsAtStartup(t *testing.T) {
	t.Parallel()

	lists := defaultTierLists()
	lists.destructive = []string{"delete_workoutz"}

	if err := startServer(t, testDeps(t), lists); !errors.Is(err, ErrUnknownTierTool) {
		t.Fatalf("start-up with a typo in the destructive list = %v, want ErrUnknownTierTool", err)
	}
}

func TestATierListThatOmitsARegisteredToolFailsAtStartup(t *testing.T) {
	t.Parallel()

	lists := defaultTierLists()
	lists.readOnly = lists.readOnly[:len(lists.readOnly)-1]

	if err := startServer(t, testDeps(t), lists); !errors.Is(err, ErrUntieredTool) {
		t.Fatalf("start-up with an incomplete read-only list = %v, want ErrUntieredTool", err)
	}
}

func TestNewRefusesIncompleteDependencies(t *testing.T) {
	t.Parallel()

	rc := testDeps(t).Client

	cases := map[string]Deps{
		"no request layer": {Caller: nopCaller{}},
		"no caller":        {Client: rc},
	}
	for name, deps := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := New(deps); !errors.Is(err, ErrMissingDependency) {
				t.Errorf("New() = %v, want ErrMissingDependency", err)
			}
		})
	}
}

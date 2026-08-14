package identity_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/identity"
)

func TestNewStdioResolverBindsExactlyOnePrincipal(t *testing.T) {
	t.Parallel()

	resolver, err := identity.NewStdioResolver(identity.StdioConfig{PrincipalIDs: []string{testPrincipalID}})
	if err != nil {
		t.Fatalf("NewStdioResolver returned error: %v", err)
	}

	got, err := resolver.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if got.ID() != testPrincipalID {
		t.Fatalf("Resolve() id = %q, want %q", got.ID(), testPrincipalID)
	}
}

func TestNewStdioResolverRefusesAmbiguousMultiAccountConfiguration(t *testing.T) {
	t.Parallel()

	_, err := identity.NewStdioResolver(identity.StdioConfig{PrincipalIDs: []string{testPrincipalID, "def456"}})
	if !errors.Is(err, identity.ErrAmbiguousPrincipal) {
		t.Fatalf("NewStdioResolver with two accounts error = %v, want ErrAmbiguousPrincipal", err)
	}
}

func TestNewStdioResolverRefusesDuplicateEntriesRatherThanDeduplicating(t *testing.T) {
	t.Parallel()

	// Two identical entries are still an operator mistake: the configuration is
	// not obviously single-account, so it is refused rather than collapsed.
	_, err := identity.NewStdioResolver(identity.StdioConfig{PrincipalIDs: []string{testPrincipalID, testPrincipalID}})
	if !errors.Is(err, identity.ErrAmbiguousPrincipal) {
		t.Fatalf("NewStdioResolver with duplicate accounts error = %v, want ErrAmbiguousPrincipal", err)
	}
}

func TestNewStdioResolverRefusesEmptyConfiguration(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		ids  []string
	}{
		{"nil slice", nil},
		{"empty slice", []string{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := identity.NewStdioResolver(identity.StdioConfig{PrincipalIDs: tc.ids})
			if !errors.Is(err, identity.ErrNoPrincipal) {
				t.Fatalf("NewStdioResolver(%v) error = %v, want ErrNoPrincipal", tc.ids, err)
			}
		})
	}
}

func TestNewStdioResolverRejectsAnEmailAsTheConfiguredPrincipal(t *testing.T) {
	t.Parallel()

	_, err := identity.NewStdioResolver(identity.StdioConfig{PrincipalIDs: []string{"athlete@example.com"}})
	if !errors.Is(err, identity.ErrEmailNotAPrincipal) {
		t.Fatalf("NewStdioResolver with an email error = %v, want ErrEmailNotAPrincipal", err)
	}
}

// The stdio principal is bound once at start-up. Resolve must therefore ignore
// everything about the incoming request, including a principal a caller planted
// on the context.
func TestStdioResolverIgnoresRequestScopedInput(t *testing.T) {
	t.Parallel()

	resolver, err := identity.NewStdioResolver(identity.StdioConfig{PrincipalIDs: []string{boundPrincipalID}})
	if err != nil {
		t.Fatalf("NewStdioResolver returned error: %v", err)
	}

	attacker, err := identity.NewPrincipal("attacker")
	if err != nil {
		t.Fatalf("NewPrincipal returned error: %v", err)
	}

	ctx := identity.WithPrincipal(context.Background(), attacker)

	got, err := resolver.Resolve(ctx)
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if got.ID() != boundPrincipalID {
		t.Fatalf("Resolve() id = %q, want the start-up bound %q", got.ID(), boundPrincipalID)
	}
}

// This is the tool-argument proof the brief demands. A tool's decoded arguments
// reach a handler only as the handler's own input value; there is no code path
// that lets an argument named user_id, email or token_path reach the resolver.
// Resolve takes a context and nothing else, so the compiler enforces it.
func TestToolArgumentsCannotSelectThePrincipal(t *testing.T) {
	t.Parallel()

	resolver, err := identity.NewStdioResolver(identity.StdioConfig{PrincipalIDs: []string{boundPrincipalID}})
	if err != nil {
		t.Fatalf("NewStdioResolver returned error: %v", err)
	}

	type argKey struct{ name string }
	hostileArguments := map[string]string{
		"user_id":       attackerPrincipalID,
		"userId":        attackerPrincipalID,
		"email":         attackerEmail,
		"account":       attackerEmail,
		"token_path":    "/tmp/attacker/garmin_tokens.json",
		"principal_id":  attackerPrincipalID,
		"principalId":   attackerPrincipalID,
		"garmin_email":  attackerEmail,
		"account_index": "1",
	}

	ctx := context.Background()
	for name, value := range hostileArguments {
		ctx = context.WithValue(ctx, argKey{name}, value)
	}

	got, err := resolver.Resolve(ctx)
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if got.ID() != boundPrincipalID {
		t.Fatalf("Resolve() id = %q, want the start-up bound %q", got.ID(), boundPrincipalID)
	}
	for name, value := range hostileArguments {
		if strings.Contains(got.ID(), value) {
			t.Fatalf("resolved principal %q reflects the hostile %s argument", got.ID(), name)
		}
	}
}

func TestStdioResolverIsSafeForConcurrentUse(t *testing.T) {
	t.Parallel()

	resolver, err := identity.NewStdioResolver(identity.StdioConfig{PrincipalIDs: []string{boundPrincipalID}})
	if err != nil {
		t.Fatalf("NewStdioResolver returned error: %v", err)
	}

	var wg sync.WaitGroup
	for range 64 {
		wg.Go(func() {
			got, resolveErr := resolver.Resolve(context.Background())
			if resolveErr != nil || got.ID() != boundPrincipalID {
				t.Errorf("Resolve() = %v, %v; want bound principal and no error", got, resolveErr)
			}
		})
	}
	wg.Wait()
}

func TestStdioResolverSatisfiesResolver(t *testing.T) {
	t.Parallel()

	resolver, err := identity.NewStdioResolver(identity.StdioConfig{PrincipalIDs: []string{boundPrincipalID}})
	if err != nil {
		t.Fatalf("NewStdioResolver returned error: %v", err)
	}

	var asInterface identity.Resolver = resolver
	if _, err := asInterface.Resolve(context.Background()); err != nil {
		t.Fatalf("Resolve through the interface returned error: %v", err)
	}
}

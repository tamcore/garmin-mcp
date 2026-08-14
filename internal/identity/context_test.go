package identity_test

import (
	"context"
	"errors"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/identity"
)

func TestFromContextReturnsThePrincipalTheServerResolved(t *testing.T) {
	t.Parallel()

	want, err := identity.NewPrincipal(testPrincipalID)
	if err != nil {
		t.Fatalf("NewPrincipal returned error: %v", err)
	}

	ctx := identity.WithPrincipal(context.Background(), want)

	got, err := identity.FromContext(ctx)
	if err != nil {
		t.Fatalf("FromContext returned error: %v", err)
	}
	if got != want {
		t.Fatalf("FromContext() = %v, want %v", got, want)
	}
}

func TestFromContextRefusesAnUnresolvedContext(t *testing.T) {
	t.Parallel()

	got, err := identity.FromContext(context.Background())
	if !errors.Is(err, identity.ErrNoPrincipal) {
		t.Fatalf("FromContext(background) error = %v, want ErrNoPrincipal", err)
	}
	if got.IsValid() {
		t.Fatal("FromContext must not fall back to a default principal")
	}
}

func TestWithPrincipalRefusesAnInvalidPrincipal(t *testing.T) {
	t.Parallel()

	var zero identity.Principal
	ctx := identity.WithPrincipal(context.Background(), zero)

	if _, err := identity.FromContext(ctx); !errors.Is(err, identity.ErrNoPrincipal) {
		t.Fatalf("FromContext after WithPrincipal(zero) error = %v, want ErrNoPrincipal", err)
	}
}

// A foreign package cannot forge a principal by planting its own context value,
// because the key type is unexported and unique to this package.
func TestForeignContextValueCannotImpersonateAPrincipal(t *testing.T) {
	t.Parallel()

	type foreignKey struct{}
	forged, err := identity.NewPrincipal("attacker")
	if err != nil {
		t.Fatalf("NewPrincipal returned error: %v", err)
	}

	ctx := context.WithValue(context.Background(), foreignKey{}, forged)

	if _, err := identity.FromContext(ctx); !errors.Is(err, identity.ErrNoPrincipal) {
		t.Fatalf("FromContext with a forged value error = %v, want ErrNoPrincipal", err)
	}
}

func TestWithPrincipalReplacesRatherThanMergesAnEarlierPrincipal(t *testing.T) {
	t.Parallel()

	first, err := identity.NewPrincipal("first")
	if err != nil {
		t.Fatalf("NewPrincipal returned error: %v", err)
	}
	second, err := identity.NewPrincipal("second")
	if err != nil {
		t.Fatalf("NewPrincipal returned error: %v", err)
	}

	ctx := identity.WithPrincipal(identity.WithPrincipal(context.Background(), first), second)

	got, err := identity.FromContext(ctx)
	if err != nil {
		t.Fatalf("FromContext returned error: %v", err)
	}
	if got != second {
		t.Fatalf("FromContext() = %v, want the most recently set principal %v", got, second)
	}
}

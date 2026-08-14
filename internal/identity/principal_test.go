package identity_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/identity"
)

// Synthetic identifiers shared by these tests.
const (
	testPrincipalID     = "abc123"
	boundPrincipalID    = "bound"
	attackerPrincipalID = "attacker-principal"
	attackerEmail       = "attacker@example.com"
)

func TestNewPrincipalAcceptsOpaqueIdentifier(t *testing.T) {
	t.Parallel()

	principal, err := identity.NewPrincipal("7f3a9c1e-0b2d-4e55-9a10-6c8d2f4b1e77")
	if err != nil {
		t.Fatalf("NewPrincipal returned error: %v", err)
	}
	if !principal.IsValid() {
		t.Fatal("a principal built from a valid identifier must report IsValid")
	}
	if got := principal.ID(); got != "7f3a9c1e-0b2d-4e55-9a10-6c8d2f4b1e77" {
		t.Fatalf("ID() = %q, want the identifier it was built from", got)
	}
}

func TestZeroPrincipalIsNeverValid(t *testing.T) {
	t.Parallel()

	var zero identity.Principal
	if zero.IsValid() {
		t.Fatal("the zero Principal must not read as valid")
	}
	if zero.ID() != "" {
		t.Fatalf("zero Principal ID() = %q, want empty", zero.ID())
	}
}

func TestNewPrincipalRejectsEmailShapedIdentifiers(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		id   string
	}{
		{"plain address", "athlete@example.com"},
		{"uppercase address", "Athlete@Example.COM"},
		{"address with plus tag", "athlete+garmin@example.com"},
		{"bare at sign", "@"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := identity.NewPrincipal(tc.id)
			if !errors.Is(err, identity.ErrEmailNotAPrincipal) {
				t.Fatalf("NewPrincipal(%q) error = %v, want ErrEmailNotAPrincipal", tc.id, err)
			}
		})
	}
}

func TestNewPrincipalRejectsMalformedIdentifiers(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		id   string
	}{
		{"empty", ""},
		{"whitespace only", "   "},
		{"leading space", " abc"},
		{"embedded newline", "abc\ndef"},
		{"embedded null", "abc\x00def"},
		{"too long", strings.Repeat("a", 257)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := identity.NewPrincipal(tc.id)
			if !errors.Is(err, identity.ErrInvalidPrincipalID) {
				t.Fatalf("NewPrincipal(%q) error = %v, want ErrInvalidPrincipalID", tc.id, err)
			}
		})
	}
}

func TestPrincipalRendersItsIdentifierOnly(t *testing.T) {
	t.Parallel()

	principal, err := identity.NewPrincipal(testPrincipalID)
	if err != nil {
		t.Fatalf("NewPrincipal returned error: %v", err)
	}

	// A pseudonymous principal id is loggable by design, so String renders it.
	if got := principal.String(); got != testPrincipalID {
		t.Fatalf("String() = %q, want %q", got, testPrincipalID)
	}
}

func TestPrincipalsCompareByValue(t *testing.T) {
	t.Parallel()

	first, err := identity.NewPrincipal(testPrincipalID)
	if err != nil {
		t.Fatalf("NewPrincipal returned error: %v", err)
	}
	second, err := identity.NewPrincipal(testPrincipalID)
	if err != nil {
		t.Fatalf("NewPrincipal returned error: %v", err)
	}
	other, err := identity.NewPrincipal("def456")
	if err != nil {
		t.Fatalf("NewPrincipal returned error: %v", err)
	}

	if first != second {
		t.Fatal("principals with the same identifier must be equal")
	}
	if first == other {
		t.Fatal("principals with different identifiers must not be equal")
	}
}

package identity_test

import (
	"context"
	"errors"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/identity"
)

// stubTokens is a VerifiedTokenSource whose answer the test controls.
type stubTokens struct {
	principal identity.Principal
	err       error
	calls     int
}

func (s *stubTokens) PrincipalFromToken(context.Context) (identity.Principal, error) {
	s.calls++
	return s.principal, s.err
}

func mustPrincipal(t *testing.T, id string) identity.Principal {
	t.Helper()

	principal, err := identity.NewPrincipal(id)
	if err != nil {
		t.Fatalf("NewPrincipal(%q) returned error: %v", id, err)
	}
	return principal
}

func TestNewBearerResolverRejectsNilSource(t *testing.T) {
	// Arrange, Act
	resolver, err := identity.NewBearerResolver(nil)

	// Assert
	if !errors.Is(err, identity.ErrNoTokenSource) {
		t.Fatalf("NewBearerResolver(nil) error = %v, want ErrNoTokenSource", err)
	}
	if resolver != nil {
		t.Fatalf("NewBearerResolver(nil) returned a resolver, want nil")
	}
}

func TestBearerResolverReturnsTokenPrincipal(t *testing.T) {
	// Arrange
	want := mustPrincipal(t, "principal-from-token")
	source := &stubTokens{principal: want}
	resolver, err := identity.NewBearerResolver(source)
	if err != nil {
		t.Fatalf("NewBearerResolver returned error: %v", err)
	}

	// Act
	got, err := resolver.Resolve(t.Context())

	// Assert
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if got != want {
		t.Fatalf("Resolve = %v, want %v", got, want)
	}
	if source.calls != 1 {
		t.Fatalf("token source consulted %d times, want 1", source.calls)
	}
}

func TestBearerResolverHasNoAnonymousFallback(t *testing.T) {
	// A source that reports no token must produce a refusal, never a default
	// principal: an unauthenticated remote request has no account to act as.

	// Arrange
	source := &stubTokens{err: errors.New("no verified token on the request")}
	resolver, err := identity.NewBearerResolver(source)
	if err != nil {
		t.Fatalf("NewBearerResolver returned error: %v", err)
	}

	// Act
	got, err := resolver.Resolve(t.Context())

	// Assert
	if !errors.Is(err, identity.ErrNoPrincipal) {
		t.Fatalf("Resolve error = %v, want ErrNoPrincipal", err)
	}
	if got.IsValid() {
		t.Fatalf("Resolve returned a valid principal %v on failure, want the zero value", got)
	}
}

func TestBearerResolverRefusesInvalidPrincipal(t *testing.T) {
	// A source that succeeds but yields the zero Principal is a broken adapter.
	// Trusting it would hand a handler an unattributed request.

	// Arrange
	source := &stubTokens{}
	resolver, err := identity.NewBearerResolver(source)
	if err != nil {
		t.Fatalf("NewBearerResolver returned error: %v", err)
	}

	// Act
	_, err = resolver.Resolve(t.Context())

	// Assert
	if !errors.Is(err, identity.ErrNoPrincipal) {
		t.Fatalf("Resolve error = %v, want ErrNoPrincipal", err)
	}
}

func TestBearerResolverIgnoresContextPlantedPrincipal(t *testing.T) {
	// A principal already on the context — however it got there — must not
	// override the token. Only the verified token decides.

	// Arrange
	planted := mustPrincipal(t, "principal-planted")
	fromToken := mustPrincipal(t, "principal-from-token")
	source := &stubTokens{principal: fromToken}
	resolver, err := identity.NewBearerResolver(source)
	if err != nil {
		t.Fatalf("NewBearerResolver returned error: %v", err)
	}
	ctx := identity.WithPrincipal(t.Context(), planted)

	// Act
	got, err := resolver.Resolve(ctx)

	// Assert
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if got != fromToken {
		t.Fatalf("Resolve = %v, want the token principal %v", got, fromToken)
	}
}

func TestBearerResolverIsAResolver(t *testing.T) {
	// Arrange
	source := &stubTokens{principal: mustPrincipal(t, "principal-a")}

	// Act
	resolver, err := identity.NewBearerResolver(source)
	if err != nil {
		t.Fatalf("NewBearerResolver returned error: %v", err)
	}

	// Assert: assignment to the interface is the assertion.
	var _ identity.Resolver = resolver
}

func TestNilBearerResolverResolvesToRefusal(t *testing.T) {
	// Arrange
	var resolver *identity.BearerResolver

	// Act
	_, err := resolver.Resolve(t.Context())

	// Assert
	if !errors.Is(err, identity.ErrNoPrincipal) {
		t.Fatalf("Resolve error = %v, want ErrNoPrincipal", err)
	}
}

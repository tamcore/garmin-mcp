package store_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/store"
)

// The operator-chosen identifier a configured client is reconciled under. It is a
// name an operator writes, not a UUID the store minted, which is the whole point of
// the reconciliation path.
const (
	configuredClientID = "example-mcp-client"
	secondRedirectURI  = "https://client.example/other-callback"
)

func configuredClient() store.ClientReconciliation {
	return store.ClientReconciliation{
		ID:           configuredClientID,
		Name:         testClientName,
		RedirectURIs: []string{testRedirectURI},
		IsPublic:     true,
	}
}

// A fresh deployment must be able to turn its configuration into rows. Without
// this, auth_transactions.client_id has no row to reference and no authorization
// can ever start.
func TestReconcileClientCreatesTheOperatorChosenIdentifier(t *testing.T) {
	sqlite, _ := newTestStore(t)
	ctx := context.Background()

	client, err := sqlite.ReconcileClient(ctx, configuredClient())
	if err != nil {
		t.Fatalf("ReconcileClient: %v", err)
	}
	if client.ID != configuredClientID {
		t.Errorf("client id is %q, want the configured %q", client.ID, configuredClientID)
	}

	read, err := sqlite.ClientByID(ctx, configuredClientID)
	if err != nil {
		t.Fatalf("ClientByID: %v", err)
	}
	if !read.HasRedirectURI(testRedirectURI) {
		t.Errorf("redirect uris are %v, want the configured one", read.RedirectURIs)
	}
	if !read.IsPublic {
		t.Error("the reconciled client is confidential, want public as configured")
	}
}

// Restarting a deployment reconciles the same configuration again, which must not
// fail and must not create a second row.
func TestReconcileClientIsIdempotentAcrossRestarts(t *testing.T) {
	sqlite, _ := newTestStore(t)
	ctx := context.Background()

	first, err := sqlite.ReconcileClient(ctx, configuredClient())
	if err != nil {
		t.Fatalf("first ReconcileClient: %v", err)
	}
	second, err := sqlite.ReconcileClient(ctx, configuredClient())
	if err != nil {
		t.Fatalf("second ReconcileClient: %v", err)
	}

	if second.ID != first.ID {
		t.Errorf("the second reconciliation reports id %q, want %q", second.ID, first.ID)
	}
	if !second.CreatedAt.Equal(first.CreatedAt) {
		t.Errorf("creation time moved from %v to %v, so the row was replaced",
			first.CreatedAt, second.CreatedAt)
	}
}

// Configuration is the source of truth: a URI added there appears, and one removed
// there stops being accepted. A stale URI that survived would be a redirect target
// the operator has withdrawn.
func TestReconcileClientAppliesTheConfiguredRedirectURIs(t *testing.T) {
	sqlite, _ := newTestStore(t)
	ctx := context.Background()

	if _, err := sqlite.ReconcileClient(ctx, configuredClient()); err != nil {
		t.Fatalf("ReconcileClient: %v", err)
	}

	widened := configuredClient()
	widened.RedirectURIs = []string{testRedirectURI, secondRedirectURI}
	if _, err := sqlite.ReconcileClient(ctx, widened); err != nil {
		t.Fatalf("widening ReconcileClient: %v", err)
	}
	read, err := sqlite.ClientByID(ctx, configuredClientID)
	if err != nil {
		t.Fatalf("ClientByID: %v", err)
	}
	if !slices.Equal(read.RedirectURIs, widened.RedirectURIs) {
		t.Errorf("redirect uris are %v, want %v", read.RedirectURIs, widened.RedirectURIs)
	}

	narrowed := configuredClient()
	narrowed.RedirectURIs = []string{secondRedirectURI}
	if _, err := sqlite.ReconcileClient(ctx, narrowed); err != nil {
		t.Fatalf("narrowing ReconcileClient: %v", err)
	}
	read, err = sqlite.ClientByID(ctx, configuredClientID)
	if err != nil {
		t.Fatalf("ClientByID after narrowing: %v", err)
	}
	if read.HasRedirectURI(testRedirectURI) {
		t.Errorf("redirect uris are %v, want the withdrawn one gone", read.RedirectURIs)
	}
	if err := sqlite.CheckRedirectURI(ctx, configuredClientID, testRedirectURI); !errors.Is(
		err, store.ErrRedirectURIMismatch) {
		t.Errorf("CheckRedirectURI on a withdrawn uri = %v, want ErrRedirectURIMismatch", err)
	}
}

// A client an operator switched off must not come back because the process
// restarted. Reconciliation reports the refusal instead, so start-up fails loudly.
func TestReconcileClientRefusesToResurrectADisabledClient(t *testing.T) {
	sqlite, _ := newTestStore(t)
	ctx := context.Background()

	if _, err := sqlite.ReconcileClient(ctx, configuredClient()); err != nil {
		t.Fatalf("ReconcileClient: %v", err)
	}
	if err := sqlite.DisableClient(ctx, configuredClientID); err != nil {
		t.Fatalf("DisableClient: %v", err)
	}

	_, err := sqlite.ReconcileClient(ctx, configuredClient())
	if !errors.Is(err, store.ErrClientDisabled) {
		t.Fatalf("ReconcileClient on a disabled client = %v, want ErrClientDisabled", err)
	}
	if _, err := sqlite.ClientByID(ctx, configuredClientID); !errors.Is(err, store.ErrClientNotFound) {
		t.Errorf("the disabled client became readable again: %v", err)
	}
}

// The public flag follows configuration in both directions. A confidential client
// that silently became public would be authenticated with no secret at all.
func TestReconcileClientNeverWidensACapabilityOnItsOwn(t *testing.T) {
	sqlite, _ := newTestStore(t)
	ctx := context.Background()

	confidential := configuredClient()
	confidential.IsPublic = false
	if _, err := sqlite.ReconcileClient(ctx, confidential); err != nil {
		t.Fatalf("ReconcileClient: %v", err)
	}

	read, err := sqlite.ClientByID(ctx, configuredClientID)
	if err != nil {
		t.Fatalf("ClientByID: %v", err)
	}
	if read.IsPublic {
		t.Error("a configured confidential client is stored as public")
	}
	// The row carries no secret, so the store can authenticate nobody against it:
	// a confidential client's credential is checked against the operator's digest
	// by the authorization server, and never here.
	if _, err := sqlite.AuthenticateClient(ctx, configuredClientID,
		store.Secret{}); !errors.Is(err, store.ErrClientNotFound) {
		t.Errorf("AuthenticateClient with no secret = %v, want ErrClientNotFound", err)
	}

	reopened := configuredClient()
	reopened.IsPublic = true
	if _, err := sqlite.ReconcileClient(ctx, reopened); err != nil {
		t.Fatalf("re-opening ReconcileClient: %v", err)
	}
	read, err = sqlite.ClientByID(ctx, configuredClientID)
	if err != nil {
		t.Fatalf("ClientByID after re-opening: %v", err)
	}
	if !read.IsPublic {
		t.Error("configuration asked for a public client and the row stayed confidential")
	}
}

// The same validation RegisterClient applies, applied here too: a reconciliation is
// a registration an operator wrote, not a way around the redirect URI rules.
func TestReconcileClientValidatesItsInput(t *testing.T) {
	sqlite, _ := newTestStore(t)
	ctx := context.Background()

	cases := map[string]store.ClientReconciliation{
		"no id":           {Name: testClientName, RedirectURIs: []string{testRedirectURI}},
		"no name":         {ID: configuredClientID, RedirectURIs: []string{testRedirectURI}},
		"no redirect uri": {ID: configuredClientID, Name: testClientName},
		"a fragment": {ID: configuredClientID, Name: testClientName,
			RedirectURIs: []string{"https://client.example/cb#fragment"}},
		"a cleartext host": {ID: configuredClientID, Name: testClientName,
			RedirectURIs: []string{"http://client.example/cb"}},
	}

	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := sqlite.ReconcileClient(ctx, spec); !errors.Is(err, store.ErrInvalidArgument) {
				t.Fatalf("ReconcileClient = %v, want ErrInvalidArgument", err)
			}
		})
	}
}

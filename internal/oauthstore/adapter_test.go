package oauthstore_test

import (
	"context"
	"errors"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/oauthserver"
	"github.com/tamcore/garmin-mcp/internal/oauthstore"
	"github.com/tamcore/garmin-mcp/internal/store"
)

// The assertion the package exists for, repeated here so a test run reports the
// break too and not only the build.
var _ oauthserver.Store = (*oauthstore.Adapter)(nil)

func TestNewRefusesMissingDependencies(t *testing.T) {
	f := newFixture(t)

	if _, err := oauthstore.New(nil, staticClients{}); err == nil {
		t.Fatal("New with no store returned no error")
	}
	if _, err := oauthstore.New(f.sqlite, nil); err == nil {
		t.Fatal("New with no client source returned no error")
	}
}

func TestClientDelegatesToTheOperatorRegistry(t *testing.T) {
	f := newFixture(t)

	client, err := f.adapter.Client(context.Background(), f.clientID)
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	if client.ID() != f.clientID {
		t.Errorf("client id is %q, want %q", client.ID(), f.clientID)
	}
	if client.MaxScopes().String() != testScopeRaw {
		t.Errorf("client scopes are %q, want %q", client.MaxScopes(), testScopeRaw)
	}
}

func TestClientReportsUnknownClient(t *testing.T) {
	f := newFixture(t)

	_, err := f.adapter.Client(context.Background(), "no-such-client")
	if !errors.Is(err, oauthserver.ErrUnknownClient) {
		t.Fatalf("error is %v, want ErrUnknownClient", err)
	}
}

// A zero Lookup is the digest of an absent credential. Addressing a row with it
// would let "no credential presented" read a record, so it is refused at the
// boundary.
func TestZeroLookupIsRefused(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	cases := map[string]func() error{
		"Transaction": func() error {
			_, err := f.adapter.Transaction(ctx, oauthserver.Lookup{})
			return err
		},
		"ConsumeTransaction": func() error {
			_, err := f.adapter.ConsumeTransaction(ctx, oauthserver.Lookup{})
			return err
		},
		"ConsumeCode": func() error {
			_, err := f.adapter.ConsumeCode(ctx, oauthserver.Lookup{})
			return err
		},
		"AccessToken": func() error {
			_, err := f.adapter.AccessToken(ctx, oauthserver.Lookup{})
			return err
		},
		"RefreshToken": func() error {
			_, err := f.adapter.RefreshToken(ctx, oauthserver.Lookup{})
			return err
		},
	}

	for name, call := range cases {
		t.Run(name, func(t *testing.T) {
			if err := call(); !errors.Is(err, oauthserver.ErrInvalidLookup) {
				t.Fatalf("error is %v, want ErrInvalidLookup", err)
			}
		})
	}
}

// A storage refusal that has no protocol meaning arrives as ErrStorage, and the
// store's own sentinel stays reachable underneath it for the log.
func TestStorageFailuresArriveAsErrStorageAndKeepTheirCause(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	transaction := f.transaction("duplicate", oauthserver.ClientState{})
	if err := f.adapter.CreateTransaction(ctx, transaction); err != nil {
		t.Fatalf("CreateTransaction: %v", err)
	}

	err := f.adapter.CreateTransaction(ctx, transaction)
	if !errors.Is(err, oauthserver.ErrStorage) {
		t.Fatalf("error is %v, want ErrStorage", err)
	}
	if !errors.Is(err, store.ErrInvalidArgument) {
		t.Fatalf("error is %v, want the store cause to stay reachable", err)
	}
}

// The adapter refuses a token pair whose two records disagree about a value the
// store writes once for both: it writes one family row from the access record, so
// the refresh record the caller returned to its client would describe a row that
// does not exist.
func TestSaveTokenPairRefusesDisagreeingRecords(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.seedConsent(t)

	cases := map[string]func(*oauthserver.RefreshToken){
		"family":    func(r *oauthserver.RefreshToken) { r.Family = "another-family" },
		"client":    func(r *oauthserver.RefreshToken) { r.ClientID = "another-client" },
		"principal": func(r *oauthserver.RefreshToken) { r.Principal = identity.Principal{} },
		"resource":  func(r *oauthserver.RefreshToken) { r.Resource = oauthserver.Resource{} },
		"scopes":    func(r *oauthserver.RefreshToken) { r.Scopes = oauthserver.ScopeSet{} },
	}

	for name, disagree := range cases {
		t.Run(name, func(t *testing.T) {
			access, refresh := f.pair(
				oauthserver.FamilyID("family-disagree-"+name), "disagree-"+name, 0)
			disagree(&refresh)
			if err := f.adapter.SaveTokenPair(ctx, access, refresh); !errors.Is(
				err, oauthserver.ErrStorage) {
				t.Fatalf("error is %v, want ErrStorage", err)
			}
		})
	}
}

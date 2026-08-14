package oauthstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/tamcore/garmin-mcp/internal/oauthserver"
	"github.com/tamcore/garmin-mcp/internal/store"
)

// ErrInvalidAdapter reports a dependency [New] refuses. It is a wiring defect, not
// a runtime condition, so it is reported once at construction and never later.
var ErrInvalidAdapter = errors.New("oauthstore: invalid adapter configuration")

// An Adapter presents the SQLite store as the storage the authorization server
// declares.
//
// It holds two immutable references and no state of its own, so it is safe for
// concurrent use by as many goroutines as the store is — which is all of them. Every
// atomicity claim it makes is the store's claim: the adapter adds no locking,
// because a lock here would be a second, weaker serialization next to the one the
// database already performs.
type Adapter struct {
	sqlite  *store.SQLiteStore
	clients oauthserver.ClientStore
}

// The assertion this package exists for. When either side changes so that the
// SQLite store can no longer serve the authorization server, this line stops
// compiling — which is the point, and the reason the fit is not recorded in a
// comment that could quietly go stale.
var _ oauthserver.Store = (*Adapter)(nil)

// New returns an adapter over sqlite, delegating client lookups to clients.
//
// Clients are not stored in SQLite. They are operator-registered configuration: the
// database holds no client scope set, no allowed resource indicators and no
// SHA-256 secret digest, so a client source is a constructor argument rather than a
// table. Both arguments are required.
func New(sqlite *store.SQLiteStore, clients oauthserver.ClientStore) (*Adapter, error) {
	if sqlite == nil {
		return nil, fmt.Errorf("oauthstore: no sqlite store: %w", ErrInvalidAdapter)
	}
	if clients == nil {
		return nil, fmt.Errorf("oauthstore: no client source: %w", ErrInvalidAdapter)
	}
	return &Adapter{sqlite: sqlite, clients: clients}, nil
}

// Client returns the registration for clientID.
//
// It delegates unchanged, including the error: the client source already speaks the
// authorization server's vocabulary, so translating here would only be a chance to
// lose ErrUnknownClient.
func (a *Adapter) Client(ctx context.Context, clientID string) (oauthserver.Client, error) {
	return a.clients.Client(ctx, clientID)
}

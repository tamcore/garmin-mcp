package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/tamcore/garmin-mcp/internal/config"
	"github.com/tamcore/garmin-mcp/internal/store"
)

// reconcileConfiguredClients makes the database hold exactly the clients
// configuration names, and then proves each one is usable.
//
// The two halves of a client registration live in two places on purpose: the
// database holds the identity and the exact redirect URIs, which an authorization
// transaction references by foreign key, and configuration holds the OAuth policy —
// the scope bound, the resource indicators and the secret digest — which the
// database has no column for. Configuration is the source of truth for both halves,
// so start-up writes the database half rather than demanding that an operator
// pre-create it: a configured client with no row can open no transaction, and there
// was no way to create that row under the operator's own identifier.
//
// Reconciliation is idempotent, applies a changed redirect URI list in both
// directions, and refuses to re-enable a client an operator disabled. That refusal
// is a start-up failure, because the alternative is a restart quietly undoing an
// operator's decision.
//
// The read-back afterwards is not redundant: it is the same lookup the
// authorization endpoint performs, so a client that reconciled but is somehow not
// readable fails here rather than at the first user's login.
func reconcileConfiguredClients(
	ctx context.Context, sqlite *store.SQLiteStore, cfg config.Config,
) error {
	for _, registration := range cfg.OAuthClients {
		if _, err := sqlite.ReconcileClient(ctx, store.ClientReconciliation{
			ID:           registration.ID,
			Name:         clientDisplayName(registration),
			RedirectURIs: registration.RedirectURIs,
			IsPublic:     registration.Public,
		}); err != nil {
			return fmt.Errorf(
				"client %q cannot be registered in the database, "+
					"so it can open no authorization transaction: %w",
				registration.ID, errors.Join(ErrUnregisteredClient, err))
		}
		if _, err := sqlite.ClientByID(ctx, registration.ID); err != nil {
			return fmt.Errorf(
				"client %q is configured but is not readable in the database: %w",
				registration.ID, errors.Join(ErrUnregisteredClient, err))
		}
	}
	return nil
}

// clientDisplayName is the name the consent screen shows. Configuration may leave
// it empty, and the store requires one, so the identifier stands in — it is the
// operator's own string and it is already validated.
func clientDisplayName(registration config.OAuthClient) string {
	if strings.TrimSpace(registration.Name) == "" {
		return registration.ID
	}
	return registration.Name
}

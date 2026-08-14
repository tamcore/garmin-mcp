package cmd

import (
	"context"

	"github.com/tamcore/garmin-mcp/internal/garmin/auth"
	"github.com/tamcore/garmin-mcp/internal/loginweb"
)

// loginSeam is the adapter between the login pages and the Garmin authenticator.
//
// It exists so internal/loginweb never sees a principal, a token, or a credential
// type: the pages hand over two strings for one call, and this binds them to the
// account the process is bound to. That is what makes an account selector
// unrepresentable in the browser flow — there is no argument to carry one.
type loginSeam struct {
	authenticator *auth.Authenticator
	principal     string
}

// loginSeam returns the adapter for this deployment's single principal.
func (d *dependencies) loginSeam() loginSeam {
	return loginSeam{authenticator: d.authenticator, principal: d.principal.ID()}
}

// Login runs one Garmin login for the bound principal.
//
// The credentials are sealed into a request-scoped value, used for this one call,
// and dropped when it returns. On success the authenticator has already written the
// encrypted DI token set; nothing about it is returned here.
func (s loginSeam) Login(ctx context.Context, email, password string) (loginweb.Attempt, error) {
	result, err := s.authenticator.Login(ctx, s.principal, auth.NewCredentials(email, password))
	if err != nil {
		return loginweb.Attempt{}, err
	}
	return attemptOf(result), nil
}

// CompleteMFA submits one one-time code for a pending continuation.
func (s loginSeam) CompleteMFA(
	ctx context.Context, transactionID, code string,
) (loginweb.Attempt, error) {
	result, err := s.authenticator.CompleteMFA(ctx, transactionID, s.principal, code)
	if err != nil {
		return loginweb.Attempt{}, err
	}
	return attemptOf(result), nil
}

// attemptOf projects a login result onto what the pages need.
//
// The transaction capability is carried across because the continuation needs it,
// and it stays inside this process: loginweb keeps it server-side and never renders
// it into a page, a path, or a query.
func attemptOf(result auth.Result) loginweb.Attempt {
	return loginweb.Attempt{
		NeedsMFA:          result.NeedsMFA(),
		TransactionID:     result.TransactionID(),
		MFAMethod:         result.MFAMethod(),
		DeliveryUncertain: result.MFADeliveryUncertain(),
		Strategy:          string(result.Strategy()),
	}
}

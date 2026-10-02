package tools

import (
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/auth"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// A token the refresh step cannot renew fails inside the transport, so the request
// layer sees no response and classifies it as unexpected. The advice must still
// name the session, not upstream drift.
func TestAdviceNamesTheSessionWhenTheTokenLayerFails(t *testing.T) {
	t.Parallel()

	for _, sentinel := range []error{auth.ErrRefreshRejected, auth.ErrNoTokens, auth.ErrNoRefreshToken} {
		err := &client.APIError{
			Op:   "get",
			Kind: client.KindUnknown,
			Err:  &url.Error{Op: "Get", URL: "https://example.invalid", Err: fmt.Errorf("refresh: %w", sentinel)},
		}
		advice := advise(err)
		if !strings.Contains(advice, "authenticate") {
			t.Errorf("advice for %v = %q, want it to name re-authentication", sentinel, advice)
		}
	}
}

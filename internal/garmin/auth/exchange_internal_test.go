package auth

import (
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
)

func TestRejectedRefreshRequiresTheRefreshOperationAndTokenEndpoint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		op       protocol.Op
		endpoint protocol.Endpoint
		want     bool
	}{
		{name: "exact refresh", op: protocol.OpRefreshToken, endpoint: protocol.EndpointDIToken, want: true},
		{name: "other operation", op: protocol.OpExchangeServiceTicket, endpoint: protocol.EndpointDIToken},
		{name: "other endpoint", op: protocol.OpRefreshToken, endpoint: protocol.EndpointSocialProfile},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := &protocol.Error{Op: tc.op, Endpoint: tc.endpoint, Status: http.StatusUnauthorized}
			if got := isRejectedRefresh(err); got != tc.want {
				t.Errorf("isRejectedRefresh() = %t, want %t", got, tc.want)
			}
		})
	}
}

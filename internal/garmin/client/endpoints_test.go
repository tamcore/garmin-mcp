package client_test

import (
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
)

func TestEndpointLabelsAreSanitized(t *testing.T) {
	t.Parallel()

	for _, endpoint := range client.KnownEndpoints() {
		if !endpoint.IsKnown() {
			t.Errorf("KnownEndpoints() reported %q, which IsKnown() rejects", string(endpoint))
		}
		label := endpoint.String()
		switch {
		case label == "":
			t.Errorf("endpoint %q renders empty", string(endpoint))
		case strings.ContainsAny(label, "/?&:= "):
			t.Errorf("endpoint label %q looks like a URL, not a label", label)
		}
	}
}

func TestUnknownEndpointRendersUnknown(t *testing.T) {
	t.Parallel()

	raw := client.Endpoint("https://connectapi.garmin.com/x?token=SENTINEL-TOKEN")
	if raw.IsKnown() {
		t.Fatal("a raw URL must not be a known endpoint label")
	}
	if got := raw.String(); got != labelUnknown {
		t.Errorf("String() = %q, want %q so a URL with a query can never be rendered", got, labelUnknown)
	}
}

func TestSocialProfileEndpointReusesProtocolLabel(t *testing.T) {
	t.Parallel()

	if string(client.EndpointSocialProfile) != string(protocol.EndpointSocialProfile) {
		t.Errorf("EndpointSocialProfile = %q, want the protocol label %q",
			string(client.EndpointSocialProfile), string(protocol.EndpointSocialProfile))
	}
}

func TestOpLabelsAreSanitized(t *testing.T) {
	t.Parallel()

	for _, op := range client.KnownOps() {
		if !op.IsKnown() {
			t.Errorf("KnownOps() reported %q, which IsKnown() rejects", string(op))
		}
		if strings.ContainsAny(op.String(), "/?&:= ") {
			t.Errorf("op label %q looks like a URL, not a label", op.String())
		}
	}
}

func TestUnknownOpRendersUnknown(t *testing.T) {
	t.Parallel()

	if got := client.Op("password=hunter2").String(); got != labelUnknown {
		t.Errorf("String() = %q, want %q", got, labelUnknown)
	}
}

func TestCredentialSubmissionOpsAreRecognized(t *testing.T) {
	t.Parallel()

	credential := []protocol.Op{
		protocol.OpMobileLogin, protocol.OpPortalLogin, protocol.OpWidgetLogin,
		protocol.OpVerifyMFA, protocol.OpRequestMFACode,
	}
	for _, op := range credential {
		converted := client.Op(op)
		if !converted.IsCredentialSubmission() {
			t.Errorf("Op(%q).IsCredentialSubmission() = false, want true", string(op))
		}
		if got := converted.String(); got != string(op) {
			t.Errorf("Op(%q).String() = %q, want the protocol label rendered verbatim", string(op), got)
		}
	}

	if client.OpListActivities.IsCredentialSubmission() {
		t.Error("OpListActivities.IsCredentialSubmission() = true, want false")
	}
}

package protocol_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
)

// A type conversion strips the method set, so String, GoString, MarshalJSON and
// LogValue no longer apply. fmt then falls into its badVerb path for a verb the
// struct does not support, and that path re-prints the value at depth 0, where a
// pointer to a struct is dereferenced and every field is shown — including
// unexported ones, because fmt reflects instead of calling a method it cannot
// reach on an unexported field.
//
// These aliases exist only to prove the material stays unreadable anyway.
type (
	strippedResponse       protocol.Response
	strippedClassification protocol.Classification
)

const (
	aliasSentinel = "SENTINEL-SERVICE-TICKET-6f2b"
	aliasCSRF     = "SENTINEL-CSRF-9a41"
)

// decimalBytes renders s the way fmt prints a []byte, because a leaked body shows
// up as decimal byte values rather than as text and stays fully recoverable.
func decimalBytes(s string) string {
	parts := make([]string, 0, len(s))
	for _, b := range []byte(s) {
		parts = append(parts, fmt.Sprintf("%d", b))
	}
	return strings.Join(parts, " ")
}

func TestMethodStrippingAliasCannotRevealResponseOrClassification(t *testing.T) {
	t.Parallel()

	body := `{"responseStatus":{"type":"SUCCESSFUL"},"serviceTicketId":"` + aliasSentinel +
		`"}<input name="_csrf" value="` + aliasCSRF + `">`
	resp := protocol.NewResponseFromParts(200, "application/json", nil, []byte(body))
	cls := protocol.ClassifyJSONLogin(resp)

	needles := map[string]string{
		"service ticket":         aliasSentinel,
		"csrf token":             aliasCSRF,
		"service ticket (bytes)": decimalBytes(aliasSentinel),
		"csrf token (bytes)":     decimalBytes(aliasCSRF),
	}

	values := map[string]any{
		"Response":       strippedResponse(resp),
		"Classification": strippedClassification(cls),
		"in a struct":    struct{ R strippedResponse }{strippedResponse(resp)},
		"in a slice":     []strippedResponse{strippedResponse(resp)},
		"in a map":       map[string]strippedClassification{"c": strippedClassification(cls)},
	}

	for name, value := range values {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%d"} {
			rendered := fmt.Sprintf(verb, value)
			for label, needle := range needles {
				if strings.Contains(rendered, needle) {
					t.Errorf("%s rendered with %s leaks the %s", name, verb, label)
				}
			}
		}
	}
}

// TestClassificationStillReportsItsSecretsToAuthorizedCallers is the counter-test.
// The fix must not become "hide everything", because the login path needs the
// ticket the accessor returns.
func TestClassificationStillReportsItsSecretsToAuthorizedCallers(t *testing.T) {
	t.Parallel()

	body := `{"responseStatus":{"type":"SUCCESSFUL"},"serviceTicketId":"` + aliasSentinel + `"}`
	cls := protocol.ClassifyJSONLogin(protocol.NewResponseFromParts(200, "application/json", nil, []byte(body)))

	if got := cls.ServiceTicket(); got != aliasSentinel {
		t.Errorf("ServiceTicket() = %q, want the ticket the accessor exists to return", got)
	}
	if cls.Outcome() != protocol.OutcomeSuccess {
		t.Errorf("Outcome() = %v, want success", cls.Outcome())
	}
}

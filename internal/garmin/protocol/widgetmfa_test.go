package protocol_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
)

// Synthetic widget page material. customerGuid identifies an account, so every
// fixture here uses an all-zero UUID rather than anything real.
const (
	fixtureGUID   = "00000000-0000-0000-0000-000000000000"
	methodEmail   = "email"
	fixtureLocale = "en_US"
	fixtureClient = "GarminConnect"
)

// widgetMFAPage renders a page carrying the inline variables Garmin's widget MFA
// page declares, with the method and codeSentTo the caller wants. The spacing is
// deliberately uneven: the parser must not depend on it.
func widgetMFAPage(method, codeSentTo string) string {
	return `<html><head><title>MFA</title></head><body><script>` +
		`var customerGuid = "` + fixtureGUID + `";` +
		"\n  var    mfaMethod=\"" + method + "\" ;\n" +
		`var locale = "` + fixtureLocale + `";` +
		`var clientId = "` + fixtureClient + `";` +
		`var codeSentTo = "` + codeSentTo + `";` +
		`</script></body></html>`
}

func widgetPageResponse(body string) protocol.Response {
	return protocol.NewResponseFromParts(http.StatusOK, "text/html", nil, []byte(body))
}

// TestWidgetMFAVarsAreParsedFromThePage pins the parse upstream drives its explicit
// code-delivery request from.
func TestWidgetMFAVarsAreParsedFromThePage(t *testing.T) {
	t.Parallel()

	class := protocol.ClassifyWidgetLogin(widgetPageResponse(widgetMFAPage(methodEmail, "")))
	if got := class.Outcome(); got != protocol.OutcomeMFARequired {
		t.Fatalf("outcome = %v, want MFA required", got)
	}

	request, ok := class.WidgetMFA()
	if !ok {
		t.Fatal("the page declared the variables and none were parsed")
	}
	if got := request.CustomerGUID(); got != fixtureGUID {
		t.Errorf("customerGuid = %q, want the page's", got)
	}
	if got := request.Method(); got != methodEmail {
		t.Errorf("mfaMethod = %q, want email", got)
	}
	if got := request.Locale(); got != fixtureLocale {
		t.Errorf("locale = %q, want %q", got, fixtureLocale)
	}
	if got := request.ClientID(); got != fixtureClient {
		t.Errorf("clientId = %q, want %q", got, fixtureClient)
	}
	if request.CodeAlreadySent() {
		t.Error("codeSentTo was empty, so no code has been sent yet")
	}
}

// TestTheParsedMethodOutranksTheTitleGuess is the behaviour the variables buy. The
// title says "MFA" either way; only the variables say which method, and an
// authenticator app has nothing to deliver.
func TestTheParsedMethodOutranksTheTitleGuess(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		method     string
		wantMethod string
		wantAsk    bool
	}{
		{methodEmail, methodEmail, true},
		{"sms", "sms", true},
		{"TOTP", "totp", false},
		{"", protocol.MFAMethodEmail, false},
	} {
		t.Run(tc.method, func(t *testing.T) {
			t.Parallel()

			class := protocol.ClassifyWidgetLogin(
				widgetPageResponse(widgetMFAPage(tc.method, "")))
			if got := class.MFAMethod(); got != tc.wantMethod {
				t.Errorf("MFAMethod() = %q, want %q", got, tc.wantMethod)
			}
			request, ok := class.WidgetMFA()
			if got := ok && request.Deliverable(); got != tc.wantAsk {
				t.Errorf("Deliverable() = %t, want %t for method %q",
					got, tc.wantAsk, tc.method)
			}
		})
	}
}

// TestACodeAlreadySentIsNotRequestedAgain matches upstream: when the sign-in POST
// already delivered a code, asking again would send the user a second one and spend
// a rate-limit budget for nothing.
func TestACodeAlreadySentIsNotRequestedAgain(t *testing.T) {
	t.Parallel()

	class := protocol.ClassifyWidgetLogin(
		widgetPageResponse(widgetMFAPage(methodEmail, "u***@example.com")))

	request, ok := class.WidgetMFA()
	if !ok {
		t.Fatal("the variables were not parsed")
	}
	if !request.CodeAlreadySent() {
		t.Error("codeSentTo was set, so Garmin has already sent a code")
	}
	if request.Deliverable() {
		t.Error("a code that was already sent must not be requested again")
	}
	if class.MFADeliveryUncertain() {
		t.Error("the page states a code was sent, so delivery is not uncertain")
	}
}

// TestAPageWithoutTheVariablesReportsNone covers the page this change must not
// affect.
//
// A page declaring no variables is one this server reads no better than it did
// before, so the title heuristic stands exactly as it was and the caller is told
// there is nothing to ask for — rather than being handed empty strings it might
// send to Garmin as an account identifier.
func TestAPageWithoutTheVariablesReportsNone(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		title         string
		wantUncertain bool
	}{
		{"MFA", false},
		{"GARMIN Authentication Application", true},
	} {
		t.Run(tc.title, func(t *testing.T) {
			t.Parallel()

			class := protocol.ClassifyWidgetLogin(widgetPageResponse(
				`<html><head><title>` + tc.title + `</title></head><body>none</body></html>`))

			if got := class.Outcome(); got != protocol.OutcomeMFARequired {
				t.Fatalf("outcome = %v, want MFA required", got)
			}
			if _, ok := class.WidgetMFA(); ok {
				t.Error("variables were reported for a page that declares none")
			}
			if got := class.MFADeliveryUncertain(); got != tc.wantUncertain {
				t.Errorf("MFADeliveryUncertain = %t, want the unchanged title rule's %t",
					got, tc.wantUncertain)
			}
			if got := class.MFAMethod(); got != protocol.MFAMethodEmail {
				t.Errorf("MFAMethod = %q, want the unchanged guess %q",
					got, protocol.MFAMethodEmail)
			}
		})
	}
}

// TestWidgetMFARequestNeverPrintsTheAccountIdentifier keeps the customer GUID out of
// logs and errors. It identifies the account, so it belongs in the request body and
// nowhere else.
func TestWidgetMFARequestNeverPrintsTheAccountIdentifier(t *testing.T) {
	t.Parallel()

	class := protocol.ClassifyWidgetLogin(widgetPageResponse(widgetMFAPage(methodEmail, "")))
	request, ok := class.WidgetMFA()
	if !ok {
		t.Fatal("the variables were not parsed")
	}

	// The alias strips the methods, which is what sends fmt down its badVerb path
	// and prints the underlying struct.
	type Raw protocol.WidgetMFARequest
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%d"} {
		for _, rendered := range []string{
			fmt.Sprintf(verb, request),
			fmt.Sprintf(verb, Raw(request)),
			fmt.Sprintf(verb, &request),
		} {
			if strings.Contains(rendered, fixtureGUID) {
				t.Errorf("the customer GUID leaked through %s: %s", verb, rendered)
			}
		}
	}
}

// widgetPageWithScript wraps arbitrary script text in an MFA-titled page.
func widgetPageWithScript(script string) string {
	return `<html><head><title>MFA</title></head><body><script>` + script +
		`</script></body></html>`
}

// TestASiblingFieldCannotCarryTheAccountIdentifier closes the way around the
// sealing.
//
// customerGuid is sealed, but mfaMethod, locale and clientId are printable: a
// method is a log field and a client id reaches the query string. A page that put
// the identifier in one of those would move it somewhere this server prints, so
// each printable field is bounded and shaped and an unusable one reads as absent.
func TestASiblingFieldCannotCarryTheAccountIdentifier(t *testing.T) {
	t.Parallel()

	class := protocol.ClassifyWidgetLogin(widgetPageResponse(widgetPageWithScript(
		`var customerGuid = "` + fixtureGUID + `";` +
			`var mfaMethod = "` + fixtureGUID + `@evil";` +
			`var locale = "` + fixtureGUID + ` ";` +
			`var clientId = "` + fixtureGUID + `/../x";` +
			`var codeSentTo = "";`)))

	request, ok := class.WidgetMFA()
	if !ok {
		t.Fatal("the variables were not parsed")
	}
	for name, got := range map[string]string{
		"Method": request.Method(), "Locale": request.Locale(), "ClientID": request.ClientID(),
	} {
		if got != "" {
			t.Errorf("%s() = %q, want empty: the value is not a plain identifier", name, got)
		}
	}
	if request.Deliverable() {
		t.Error("a page this malformed must not produce a delivery request")
	}
	if class.MFADeliveryUncertain() {
		t.Error("nothing is deliverable here, so nothing is pending delivery")
	}
}

// TestAnIncompletePageIsNotDeliverable refuses to send Garmin a request built from
// gaps. One variable on a page is not a delivery request.
func TestAnIncompletePageIsNotDeliverable(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		script string
	}{
		{"no customer guid", `var mfaMethod = "email";var clientId = "c";`},
		{"no client id", `var customerGuid = "` + fixtureGUID + `";var mfaMethod = "email";`},
		{"no method", `var customerGuid = "` + fixtureGUID + `";var clientId = "c";`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			class := protocol.ClassifyWidgetLogin(
				widgetPageResponse(widgetPageWithScript(tc.script)))
			request, ok := class.WidgetMFA()
			if !ok {
				t.Fatal("the variables were not parsed")
			}
			if request.Deliverable() {
				t.Error("an incomplete page produced a delivery request")
			}
		})
	}
}

// TestAnAmbiguousPageIsTreatedAsDeclaringNothing covers a second declaration
// disagreeing with the first.
//
// The parser reads the whole document, so a later "var codeSentTo" in an unrelated
// script or a comment would decide whether a code is requested at all. A page whose
// own variables disagree is not a page to act on.
func TestAnAmbiguousPageIsTreatedAsDeclaringNothing(t *testing.T) {
	t.Parallel()

	class := protocol.ClassifyWidgetLogin(widgetPageResponse(widgetPageWithScript(
		`var customerGuid = "` + fixtureGUID + `";` +
			`var mfaMethod = "email";` +
			`var clientId = "` + fixtureClient + `";` +
			`var codeSentTo = "";` +
			`var codeSentTo = "u***@example.com";`)))

	if _, ok := class.WidgetMFA(); ok {
		t.Error("a page whose variables disagree was treated as declaring them")
	}
}

// TestARepeatedButAgreeingDeclarationIsFine keeps the rule narrow: repetition alone
// is not ambiguity, and Garmin's page may legitimately restate a value.
func TestARepeatedButAgreeingDeclarationIsFine(t *testing.T) {
	t.Parallel()

	class := protocol.ClassifyWidgetLogin(widgetPageResponse(widgetPageWithScript(
		`var customerGuid = "` + fixtureGUID + `";` +
			`var mfaMethod = "email";` +
			`var clientId = "` + fixtureClient + `";` +
			`var mfaMethod = "email";` +
			`var codeSentTo = "";`)))

	request, ok := class.WidgetMFA()
	if !ok {
		t.Fatal("an agreeing repetition was treated as ambiguous")
	}
	if !request.Deliverable() {
		t.Error("the page is complete and deliverable")
	}
}

// TestAnAuthenticatorAppIsNotPendingDelivery pins the meaning of the uncertainty
// flag: it marks a code that should arrive and might not. A TOTP page has no code
// in flight, so telling the caller to wait would be wrong.
func TestAnAuthenticatorAppIsNotPendingDelivery(t *testing.T) {
	t.Parallel()

	class := protocol.ClassifyWidgetLogin(widgetPageResponse(widgetMFAPage("totp", "")))
	if class.MFADeliveryUncertain() {
		t.Error("an authenticator app has nothing in flight to be uncertain about")
	}
}

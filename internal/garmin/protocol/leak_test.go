// This test file is deliberately in the external test package: it asserts what a
// package that only imports protocol can and cannot reach.
package protocol_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
)

// Synthetic secret-shaped material. None of it may reach any rendered form.
const (
	leakBody   = `{"password":"S3cr3t-Passw0rd","serviceTicketId":"ST-secret-0200"}`
	leakTicket = "ST-secret-0200"
	leakCSRF   = "csrf-secret-0201"
	leakCookie = "GARMIN-SSO-GUID=cookie-secret-0202"
	leakBearer = "Bearer bearer-secret-0203"
	leakTitle  = "title-secret-0204"
	leakPass   = "S3cr3t-Passw0rd"
)

func leakedStrings() []string {
	return []string{
		leakBody, leakTicket, leakCSRF, leakCookie, leakBearer, leakTitle, leakPass,
		"cookie-secret-0202", "bearer-secret-0203",
	}
}

func assertNoLeak(t *testing.T, form, rendered string) {
	t.Helper()

	for _, bad := range leakedStrings() {
		if strings.Contains(rendered, bad) {
			t.Fatalf("%s rendering %q leaked %q", form, rendered, bad)
		}
	}
}

func secretHeader() http.Header {
	header := http.Header{}
	header.Set("Cookie", leakCookie)
	header.Set("Authorization", leakBearer)
	header.Set("Set-Cookie", leakCookie)
	header.Set("Retry-After", "5")
	return header
}

func secretResponse() protocol.Response {
	return protocol.NewResponseFromParts(http.StatusOK, "application/json", secretHeader(), []byte(leakBody))
}

// secretClassification is produced by the classifier, which is the only way a
// Classification is ever built in production.
func secretClassification() protocol.Classification {
	body := fmt.Sprintf(
		`<html><head><title>%s</title></head><body>`+
			`<input name="_csrf" value="%s"/><a href="/x?ticket=%s">go</a></body></html>`,
		leakTitle, leakCSRF, leakTicket)

	return protocol.ClassifyWidgetLogin(
		protocol.NewResponseFromParts(http.StatusOK, "text/html", nil, []byte(body)))
}

// ticketClassification is a successful widget verdict, which is the only shape
// that carries a service ticket.
func ticketClassification() protocol.Classification {
	body := fmt.Sprintf(
		`<html><head><title>Success</title></head><body>`+
			`<a href="/x?ticket=%s">go</a></body></html>`, leakTicket)

	return protocol.ClassifyWidgetLogin(
		protocol.NewResponseFromParts(http.StatusOK, "text/html", nil, []byte(body)))
}

// A method-stripping alias defeats String, GoString and MarshalJSON. It must
// still be unable to reach the secret material, which means the material cannot
// live in a field that reflection can read.
func TestMethodStrippingAliasCannotReachSecrets(t *testing.T) {
	t.Parallel()

	type rawResponse protocol.Response
	type rawClassification protocol.Classification

	resp := rawResponse(secretResponse())
	class := rawClassification(secretClassification())

	// Marshalled through any: having no exported fields is the property under
	// test, so the encoder must be handed the value the way a logger would.
	respJSON, err := marshalOpaque(resp)
	if err != nil {
		t.Fatalf("json.Marshal(alias of Response) error: %v", err)
	}
	classJSON, err := marshalOpaque(class)
	if err != nil {
		t.Fatalf("json.Marshal(alias of Classification) error: %v", err)
	}

	renderings := map[string]string{
		"alias Response %v":         fmt.Sprintf("%v", resp),
		"alias Response %+v":        fmt.Sprintf("%+v", resp),
		"alias Response %#v":        fmt.Sprintf("%#v", resp),
		"alias Response json":       string(respJSON),
		"alias Classification %v":   fmt.Sprintf("%v", class),
		"alias Classification %+v":  fmt.Sprintf("%+v", class),
		"alias Classification %#v":  fmt.Sprintf("%#v", class),
		"alias Classification json": string(classJSON),
	}
	for form, rendered := range renderings {
		assertNoLeak(t, form, rendered)
	}
}

func marshalOpaque(value any) ([]byte, error) { return json.Marshal(value) }

// A reflection-based logger walks exported fields. There must be none that carry
// secret material.
func TestSecretBearingTypesExposeNoExportedFields(t *testing.T) {
	t.Parallel()

	for _, typ := range []reflect.Type{
		reflect.TypeFor[protocol.Response](),
		reflect.TypeFor[protocol.Classification](),
	} {
		for field := range typ.Fields() {
			if field.IsExported() {
				t.Fatalf("%s.%s is exported; a reflective logger can read it", typ.Name(), field.Name)
			}
		}
	}
}

func TestSlogJSONHandlerEmitsNoSecretMaterial(t *testing.T) {
	t.Parallel()

	type record struct {
		Attempt        int
		Response       protocol.Response
		Classification protocol.Classification
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	logger.Info("login attempt",
		"response", secretResponse(),
		"classification", secretClassification(),
		"record", record{Attempt: 1, Response: secretResponse(), Classification: secretClassification()},
	)

	assertNoLeak(t, "slog json", buf.String())
	for _, want := range []string{"bodyBytes", "serviceTicketPresent"} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("slog output %q lost the redacted field %q", buf.String(), want)
		}
	}
}

func TestSlogTextHandlerEmitsNoSecretMaterial(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, leakLogOptions()))
	logger.Info("login attempt", "response", secretResponse(), "classification", secretClassification())

	assertNoLeak(t, "slog text", buf.String())
}

func TestSecretBearingTypesImplementLogValuer(t *testing.T) {
	t.Parallel()

	var _ slog.LogValuer = protocol.Response{}
	var _ slog.LogValuer = protocol.Classification{}
}

func methodNames(typ reflect.Type) []string {
	out := make([]string, 0, typ.NumMethod())
	for method := range typ.Methods() {
		out = append(out, method.Name)
	}
	sort.Strings(out)
	return out
}

// The accessor set is pinned: every method is either a redacted rendering, a
// copy-returning setter, or a value a caller genuinely needs. Adding one is a
// deliberate decision, not an accident.
func TestExportedMethodSetsArePinned(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		typ  reflect.Type
		want []string
	}{
		{
			name: "Response",
			typ:  reflect.TypeFor[protocol.Response](),
			want: []string{
				"BodyLen", "ContentType", "GoString", "HeaderLen", "LogValue",
				"MarshalJSON", "Status", "String", "WithNow",
			},
		},
		{
			name: "Classification",
			typ:  reflect.TypeFor[protocol.Classification](),
			want: []string{
				"CSRFToken", "Err", "GoString", "LogValue", "MFADeliveryUncertain",
				"MFAMethod", "MarshalJSON", "Outcome", "PageTitle", "ResponseStatusType",
				"RetryAfter", "ServiceTicket", "Status", "String", "WidgetMFA",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := methodNames(tc.typ)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("%s method set = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

// The accessors must still hand a caller the values it needs to drive the login
// flow: without them the classifier is useless.
func TestAccessorsExposeWhatCallersNeed(t *testing.T) {
	t.Parallel()

	resp := secretResponse()
	if got := resp.Status(); got != http.StatusOK {
		t.Fatalf("Response.Status() = %d, want %d", got, http.StatusOK)
	}
	if got := resp.ContentType(); got != "application/json" {
		t.Fatalf("Response.ContentType() = %q, want application/json", got)
	}
	if got := resp.BodyLen(); got != len(leakBody) {
		t.Fatalf("Response.BodyLen() = %d, want %d", got, len(leakBody))
	}
	if got := resp.HeaderLen(); got != 4 {
		t.Fatalf("Response.HeaderLen() = %d, want 4", got)
	}

	class := secretClassification()
	if got := class.CSRFToken(); got != leakCSRF {
		t.Fatalf("Classification.CSRFToken() = %q, want %q", got, leakCSRF)
	}
	if got := class.PageTitle(); got != leakTitle {
		t.Fatalf("Classification.PageTitle() = %q, want %q", got, leakTitle)
	}

	ticketed := ticketClassification()
	if got := ticketed.Outcome(); got != protocol.OutcomeSuccess {
		t.Fatalf("Classification.Outcome() = %v, want %v", got, protocol.OutcomeSuccess)
	}
	if got := ticketed.ServiceTicket(); got != leakTicket {
		t.Fatalf("Classification.ServiceTicket() = %q, want %q", got, leakTicket)
	}
}

// A zero Response and a zero Classification are inert, so an accessor on a value
// a caller declared but never filled cannot panic.
func TestZeroValuesAreInert(t *testing.T) {
	t.Parallel()

	var resp protocol.Response
	var class protocol.Classification

	if resp.Status() != 0 || resp.BodyLen() != 0 || resp.HeaderLen() != 0 || resp.ContentType() != "" {
		t.Fatalf("zero Response is not inert: %s", resp.String())
	}
	if class.Outcome() != protocol.OutcomeUnknown || class.ServiceTicket() != "" {
		t.Fatalf("zero Classification is not inert: %s", class.String())
	}
	if _, err := json.Marshal(resp); err != nil {
		t.Fatalf("json.Marshal(zero Response) error: %v", err)
	}
	if _, err := json.Marshal(class); err != nil {
		t.Fatalf("json.Marshal(zero Classification) error: %v", err)
	}
	if err := class.Err(protocol.OpMobileLogin, protocol.EndpointMobileLogin, nil); err == nil {
		t.Fatal("zero Classification must not report success")
	}
}

func TestNewResponseFromHTTPResponse(t *testing.T) {
	t.Parallel()

	httpResp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Content-Type": []string{"application/json; charset=utf-8"}},
	}

	resp := protocol.NewResponse(httpResp, []byte(leakBody))
	if got := resp.Status(); got != http.StatusTooManyRequests {
		t.Fatalf("Status() = %d, want %d", got, http.StatusTooManyRequests)
	}
	if got := resp.ContentType(); got != "application/json" {
		t.Fatalf("ContentType() = %q, want the sanitized media type", got)
	}
	if got := resp.BodyLen(); got != len(leakBody) {
		t.Fatalf("BodyLen() = %d, want %d", got, len(leakBody))
	}
	assertNoLeak(t, "NewResponse String()", resp.String())

	if got := protocol.NewResponse(nil, nil).Status(); got != 0 {
		t.Fatalf("NewResponse(nil, nil).Status() = %d, want 0", got)
	}
}

// The constructors must not retain the caller's header map, so a later mutation
// cannot change an already-classified response.
func TestNewResponseCopiesTheHeader(t *testing.T) {
	t.Parallel()

	header := secretHeader()
	resp := protocol.NewResponseFromParts(http.StatusOK, "application/json", header, nil)
	header.Set("Retry-After", "999")

	if got := resp.HeaderLen(); got != 4 {
		t.Fatalf("HeaderLen() = %d, want 4", got)
	}
	if got := protocol.ClassifyJSONLogin(resp).RetryAfter(); got != 5*time.Second {
		t.Fatalf("RetryAfter() = %v, want 5s; the header was not copied", got)
	}
}

// leakLogOptions drops the timestamp before a leak assertion sees the line.
//
// A log line carries its own wall clock, and a numeric needle collides with it: at
// 18:16:11.596Z the line contains "11.5", so a test looking for the fixture value
// 11.5 reports a leak that is not there. It has fired on CI once already. Dropping
// the attribute narrows the haystack to what the model itself rendered, which is
// what these tests are about.
func leakLogOptions() *slog.HandlerOptions {
	return &slog.HandlerOptions{
		Level: slog.LevelDebug,
		ReplaceAttr: func(groups []string, attr slog.Attr) slog.Attr {
			if len(groups) == 0 && attr.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return attr
		},
	}
}

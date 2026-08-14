package testkit

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
)

// TestDoerHasNoExportedFieldsToSwap is the structural half of the guarantee: a
// caller cannot disable the origin guard because the value handed out carries no
// field it could reassign.
func TestDoerHasNoExportedFieldsToSwap(t *testing.T) {
	t.Parallel()

	srv := NewServer(t, NewScript())

	typ := reflect.TypeOf(srv.Doer())
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct {
		t.Fatalf("Doer concrete kind = %v, want a struct", typ.Kind())
	}
	for field := range typ.Fields() {
		if field.IsExported() {
			t.Fatalf("Doer exposes field %s, which a caller could reassign", field.Name)
		}
	}
}

// TestDoerCannotBeConvertedToAnHTTPClient proves the returned value is not an
// *http.Client in disguise, so Transport and CheckRedirect cannot be replaced.
func TestDoerCannotBeConvertedToAnHTTPClient(t *testing.T) {
	t.Parallel()

	srv := NewServer(t, NewScript())
	doer := srv.Doer()

	if _, ok := doer.(*http.Client); ok {
		t.Fatal("Doer is an *http.Client, so Transport and CheckRedirect are swappable")
	}
	if _, ok := doer.(interface{ Transport() http.RoundTripper }); ok {
		t.Fatal("Doer hands out its transport")
	}
}

// TestDoerOnlyExposesDo keeps the surface minimal: extra methods are extra ways
// to reach the network.
func TestDoerOnlyExposesDo(t *testing.T) {
	t.Parallel()

	srv := NewServer(t, NewScript())

	typ := reflect.TypeOf(srv.Doer())
	for method := range typ.Methods() {
		if name := method.Name; name != "Do" {
			t.Fatalf("Doer exposes method %s, want Do only", name)
		}
	}
}

func TestDoerWithTimeoutBoundsOneRequest(t *testing.T) {
	t.Parallel()

	srv := NewServer(t, NewScript().With(
		protocol.PathMobileLogin,
		JSON(http.StatusOK, LoginSuccessJSON("ST-fake-3010")).WithDelay(300*time.Millisecond),
	))

	loginURL := srv.Hosts(protocol.DomainGlobal).MobileLoginURL()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, loginURL, strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := srv.Doer(WithTimeout(20 * time.Millisecond)).Do(req)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("expected a timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("err = %v, want a timeout", err)
	}
}

func TestDoerWithoutTimeoutIsUnbounded(t *testing.T) {
	t.Parallel()

	srv := NewServer(t, NewScript().With(
		protocol.PathMobileLogin,
		JSON(http.StatusOK, LoginSuccessJSON("ST-fake-3011")).WithDelay(50*time.Millisecond),
	))

	got := protocol.ClassifyJSONLogin(post(t, srv.Doer(),
		srv.Hosts(protocol.DomainGlobal).MobileLoginURL(), ContentTypeJSON, "{}"))
	if got.ServiceTicket() != "ST-fake-3011" {
		t.Fatalf("ServiceTicket = %q, want ST-fake-3011", got.ServiceTicket())
	}
}

// TestDoersDoNotShareState checks one Doer's timeout cannot leak into another.
func TestDoersDoNotShareState(t *testing.T) {
	t.Parallel()

	srv := NewServer(t, NewScript().With(
		protocol.PathMobileLogin,
		JSON(http.StatusOK, LoginSuccessJSON("ST-fake-3012")).WithDelay(60*time.Millisecond),
	))
	loginURL := srv.Hosts(protocol.DomainGlobal).MobileLoginURL()

	if err := doRaw(srv.Doer(WithTimeout(time.Millisecond)), requestTo(t, loginURL)); err == nil {
		t.Fatal("the bounded Doer did not time out")
	}

	got := protocol.ClassifyJSONLogin(post(t, srv.Doer(), loginURL, ContentTypeJSON, "{}"))
	if got.ServiceTicket() != "ST-fake-3012" {
		t.Fatalf("ServiceTicket = %q, want ST-fake-3012", got.ServiceTicket())
	}
}

func TestDoerRefusesNilRequest(t *testing.T) {
	t.Parallel()

	srv := NewServer(t, NewScript())

	resp, err := srv.Doer().Do(nil)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil {
		t.Fatal("Do(nil) succeeded, want an error")
	}
}

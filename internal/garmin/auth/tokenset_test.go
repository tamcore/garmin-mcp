// This test file is deliberately in the external test package: it asserts what a
// package that only imports auth can and cannot reach.
package auth_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/auth"
)

// Synthetic secret-shaped material. None of it may reach any rendered form.
const (
	leakToken    = "di-token-secret-0300"
	leakRefresh  = "di-refresh-secret-0301"
	leakPassword = "S3cr3t-Passw0rd-0305"
	leakEmail    = "leak-user@example.invalid"
	leakCode     = "998877"
)

// Labels for the rendered forms every redaction test walks.
const (
	formString   = "String"
	formGoString = "GoString"
	formV        = "%v"
	formPlusV    = "%+v"
	formHashV    = "%#v"
	formSlog     = "slog"
)

func secretTokenSet() auth.TokenSet {
	return auth.NewTokenSet(leakToken, leakRefresh, "GARMIN_CONNECT_MOBILE_ANDROID_DI",
		time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC))
}

func tokenLeakStrings() []string {
	return []string{leakToken, leakRefresh, leakEmail, leakCode, leakPassword, "secret-0300", "secret-0301"}
}

func assertNoTokenLeak(t *testing.T, form, rendered string) {
	t.Helper()

	for _, bad := range tokenLeakStrings() {
		if strings.Contains(rendered, bad) {
			t.Fatalf("%s rendering %q leaked %q", form, rendered, bad)
		}
	}
}

func TestTokenSetAccessors(t *testing.T) {
	expires := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	set := auth.NewTokenSet(leakToken, leakRefresh, "GARMIN_CONNECT_MOBILE_ANDROID_DI", expires)

	if set.Token() != leakToken {
		t.Errorf("Token() = %q", set.Token())
	}
	if set.RefreshToken() != leakRefresh {
		t.Errorf("RefreshToken() = %q", set.RefreshToken())
	}
	if set.ClientID() != "GARMIN_CONNECT_MOBILE_ANDROID_DI" {
		t.Errorf("ClientID() = %q", set.ClientID())
	}
	if !set.ExpiresAt().Equal(expires) {
		t.Errorf("ExpiresAt() = %v, want %v", set.ExpiresAt(), expires)
	}
	if set.IsZero() {
		t.Error("IsZero() = true for a populated set")
	}
}

func TestTokenSetZeroValueIsInert(t *testing.T) {
	var set auth.TokenSet

	if !set.IsZero() {
		t.Error("IsZero() = false for the zero value")
	}
	if set.Token() != "" || set.RefreshToken() != "" || set.ClientID() != "" {
		t.Error("zero value reports non-empty secrets")
	}
	if !set.ExpiresAt().IsZero() {
		t.Error("zero value reports a non-zero expiry")
	}
	assertNoTokenLeak(t, "zero String", set.String())
}

func TestTokenSetWithRotatedDoesNotMutateReceiver(t *testing.T) {
	original := secretTokenSet()
	later := original.ExpiresAt().Add(time.Hour)

	rotated := original.WithRotated("di-token-secret-0302", "di-refresh-secret-0303", later)

	if original.Token() != leakToken || original.RefreshToken() != leakRefresh {
		t.Fatal("WithRotated mutated its receiver")
	}
	if rotated.Token() != "di-token-secret-0302" {
		t.Errorf("rotated Token() = %q", rotated.Token())
	}
	if rotated.RefreshToken() != "di-refresh-secret-0303" {
		t.Errorf("rotated RefreshToken() = %q", rotated.RefreshToken())
	}
	if rotated.ClientID() != original.ClientID() {
		t.Errorf("rotated ClientID() = %q, want %q", rotated.ClientID(), original.ClientID())
	}
	if !rotated.ExpiresAt().Equal(later) {
		t.Errorf("rotated ExpiresAt() = %v, want %v", rotated.ExpiresAt(), later)
	}
}

func TestTokenSetWithRotatedKeepsRefreshTokenWhenGarminOmitsIt(t *testing.T) {
	original := secretTokenSet()

	rotated := original.WithRotated("di-token-secret-0304", "", original.ExpiresAt())

	if rotated.RefreshToken() != leakRefresh {
		t.Errorf("RefreshToken() = %q, want the previous token kept", rotated.RefreshToken())
	}
}

// TestTokenSetRenderingIsRedacted covers every route a token could reach a log.
func TestTokenSetRenderingIsRedacted(t *testing.T) {
	set := secretTokenSet()

	assertNoTokenLeak(t, "String", set.String())
	assertNoTokenLeak(t, "GoString", set.GoString())
	assertNoTokenLeak(t, "%v", fmt.Sprintf("%v", set))
	assertNoTokenLeak(t, "%+v", fmt.Sprintf("%+v", set))
	assertNoTokenLeak(t, "%#v", fmt.Sprintf("%#v", set))
	assertNoTokenLeak(t, "%s", fmt.Sprintf("set=%s.", set))

	encoded, err := json.Marshal(set)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	assertNoTokenLeak(t, "json.Marshal", string(encoded))

	assertNoTokenLeak(t, "json.Marshal in a struct", marshalNested(t, set))
	assertNoTokenLeak(t, "slog", logLine(t, "tokens", set))
	assertNoTokenLeak(t, "slog pointer", logLine(t, "tokens", &set))
}

func marshalNested(t *testing.T, set auth.TokenSet) string {
	t.Helper()

	encoded, err := json.Marshal(struct {
		Tokens auth.TokenSet `json:"tokens"`
	}{Tokens: set})
	if err != nil {
		t.Fatalf("json.Marshal nested: %v", err)
	}
	return string(encoded)
}

func logLine(t *testing.T, key string, value any) string {
	t.Helper()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	logger.Info("auth event", slog.Any(key, value))
	return buf.String()
}

// TestTokenSetAliasCannotStripRedaction proves the method-stripping trick fails:
// a defined type with auth.TokenSet's underlying type has no methods, so fmt
// falls back to reflection, which must find no readable secret.
func TestTokenSetAliasCannotStripRedaction(t *testing.T) {
	type stripped auth.TokenSet

	set := secretTokenSet()

	assertNoTokenLeak(t, "stripped %v", fmt.Sprintf("%v", stripped(set)))
	assertNoTokenLeak(t, "stripped %+v", fmt.Sprintf("%+v", stripped(set)))
	assertNoTokenLeak(t, "stripped %#v", fmt.Sprintf("%#v", stripped(set)))

	// SA9005 is exactly the property under test: the alias has no exported field
	// and no marshaler, so nothing can be serialized out of it.
	encoded, err := json.Marshal(stripped(set)) //nolint:staticcheck
	if err != nil {
		t.Fatalf("json.Marshal stripped: %v", err)
	}
	assertNoTokenLeak(t, "stripped json.Marshal", string(encoded))
}

// TestTokenSetHasNoExportedOrStringFields proves the secrets are unreachable by
// reflection: every field is unexported and none has a string kind, so a
// reflective logger walking the value cannot read a token.
func TestTokenSetHasNoExportedOrStringFields(t *testing.T) {
	typ := reflect.TypeFor[auth.TokenSet]()

	for field := range typ.Fields() {
		if field.IsExported() {
			t.Errorf("field %s is exported", field.Name)
		}
		if field.Type.Kind() == reflect.String {
			t.Errorf("field %s has string kind, so reflection can read it", field.Name)
		}
	}
}

func TestCredentialsRenderingIsRedacted(t *testing.T) {
	creds := auth.NewCredentials(leakEmail, leakPassword)

	if creds.Email() != leakEmail {
		t.Errorf("Email() = %q", creds.Email())
	}
	if creds.Password() != leakPassword {
		t.Error("Password() did not round-trip")
	}

	for form, rendered := range map[string]string{
		formString:   creds.String(),
		formGoString: creds.GoString(),
		formV:        fmt.Sprintf("%v", creds),
		formPlusV:    fmt.Sprintf("%+v", creds),
		formHashV:    fmt.Sprintf("%#v", creds),
		formSlog:     logLine(t, "credentials", creds),
	} {
		assertNoTokenLeak(t, form, rendered)
	}

	encoded, err := json.Marshal(creds)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	assertNoTokenLeak(t, "json.Marshal", string(encoded))
}

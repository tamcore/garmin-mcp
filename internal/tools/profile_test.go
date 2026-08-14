package tools_test

import (
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
	"github.com/tamcore/garmin-mcp/internal/tools"
)

func TestGetUserProfileReturnsTheFlatProfile(t *testing.T) {
	h := newHarness(t, readScript())

	got := h.call(t, tools.ToolGetUserProfile, nil)

	if got["display_name"] != testDisplayName {
		t.Errorf("display_name = %v, want %q", got["display_name"], testDisplayName)
	}
	if got["full_name"] != testFullName {
		t.Errorf("full_name = %v, want %q", got["full_name"], testFullName)
	}
	if got["profile_id"] != float64(900001) {
		t.Errorf("profile_id = %v, want 900001", got["profile_id"])
	}
}

func TestGetFullNameReturnsOnlyTheName(t *testing.T) {
	h := newHarness(t, readScript())

	got := h.call(t, tools.ToolGetFullName, nil)

	if got["full_name"] != testFullName {
		t.Errorf("full_name = %v, want %q", got["full_name"], testFullName)
	}
	if len(got) != 1 {
		t.Errorf("get_full_name returned %d keys (%v), want only full_name", len(got), got)
	}
}

func TestGetUnitSystemReturnsTheMeasurementSystem(t *testing.T) {
	h := newHarness(t, readScript())

	got := h.call(t, tools.ToolGetUnitSystem, nil)

	if got["unit_system"] != "metric" {
		t.Errorf("unit_system = %v, want %q", got["unit_system"], "metric")
	}
	if len(got) != 1 {
		t.Errorf("get_unit_system returned %d keys (%v), want only unit_system", len(got), got)
	}
}

func TestProfileToolsRejectAnyArgument(t *testing.T) {
	h := newHarness(t, readScript())

	for _, name := range []string{tools.ToolGetUserProfile, tools.ToolGetFullName, tools.ToolGetUnitSystem} {
		t.Run(name, func(t *testing.T) {
			text := h.callError(t, name, map[string]any{"user_id": "someone-else"})
			assertSanitized(t, text)
		})
	}
}

func TestGetUnitSystemReportsAnUnsetMeasurementSystemAsAnError(t *testing.T) {
	script := testkit.NewScript().
		With(client.PathUserSettings, testkit.JSON(http.StatusOK, `{"id":900001,"userData":{}}`))
	h := newHarness(t, script)

	text := h.callError(t, tools.ToolGetUnitSystem, nil)

	assertSanitized(t, text)
	if text == "" {
		t.Error("the refusal carries no text")
	}
}

func TestAGarminFailureBecomesASanitizedActionableError(t *testing.T) {
	script := testkit.NewScript().
		With(client.PathSocialProfile, testkit.JSON(http.StatusUnauthorized,
			`{"error":"session expired","cookie":"GARMIN-SSO=secret-cookie-value",`+
				`"access_token":"eyJhbGciOiJSUzI1NiJ9.secret-token"}`))
	h := newHarness(t, script)

	text := h.callError(t, tools.ToolGetUserProfile, nil)

	assertSanitized(t, text)
	if !containsFold(text, "authenticate") {
		t.Errorf("the refusal %q does not tell the caller to re-authenticate", text)
	}
}

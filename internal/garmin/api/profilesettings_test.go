package api_test

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

const profileSettingsBody = `{"id":900001,"profileId":900001,"displayName":"fake-tester",` +
	`"fullName":"Fake Tester","measurementSystem":"metric","powerFormat":{"unitKey":"watt"},` +
	`"surpriseField":[1,2,3]}`

const personalRecordsBody = `[{"id":1,"typeId":1,"activityId":18446744,"value":1234.5,` +
	`"prStartTimeLocal":"2026-01-31T09:00:00.0","activityName":"Morning Run",` +
	`"unknownField":{"nested":true}}]`

// personalRecordsPath is the display-name-keyed personal-record path.
func personalRecordsPath() string {
	return client.PathPersonalRecords + "/" + url.PathEscape(fakeDisplayName)
}

// TestProfileSettingsAndDerivedReads covers the profile settings document and the
// two values upstream serves from its login-time cache.
func TestProfileSettingsAndDerivedReads(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().
		With(client.PathUserProfileSettings, testkit.JSON(http.StatusOK, profileSettingsBody)).
		With(client.PathUserSettings, testkit.JSON(http.StatusOK, userSettingsBody)).
		With(client.PathSocialProfile, testkit.JSON(http.StatusOK, socialProfileBody))
	h := newHarness(t, script, client.Limits{})
	profile := newProfile(t, h)

	settings, err := profile.ProfileSettings(t.Context(), h.session)
	if err != nil {
		t.Fatalf("ProfileSettings() = %v", err)
	}
	if settings.FullName == nil || *settings.FullName != fakeFullName {
		t.Errorf("FullName = %v, want the fixture name", settings.FullName)
	}
	if settings.Payload().Len() == 0 {
		t.Error("ProfileSettings() retained no raw payload")
	}

	system, err := profile.UnitSystem(t.Context(), h.session)
	if err != nil {
		t.Fatalf("UnitSystem() = %v", err)
	}
	if system != "metric" {
		t.Errorf("UnitSystem() = %q, want metric", system)
	}

	name, err := profile.FullName(t.Context(), h.session)
	if err != nil {
		t.Fatalf("FullName() = %v", err)
	}
	if name != fakeFullName {
		t.Errorf("FullName() = %q, want the fixture name", name)
	}
}

// TestDerivedProfileReadsReportAMissingField rather than inventing one.
func TestDerivedProfileReadsReportAMissingField(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().
		With(client.PathUserSettings, testkit.JSON(http.StatusOK, `{"id":900001}`)).
		With(client.PathSocialProfile, testkit.JSON(http.StatusOK,
			`{"profileId":900001,"displayName":"fake-tester"}`))
	h := newHarness(t, script, client.Limits{})
	profile := newProfile(t, h)

	if _, err := profile.UnitSystem(t.Context(), h.session); !errors.Is(
		err, client.ErrMalformedPayload) {
		t.Errorf("UnitSystem() without userData = %v, want ErrMalformedPayload", err)
	}
	if _, err := profile.FullName(t.Context(), h.session); !errors.Is(
		err, client.ErrMalformedPayload) {
		t.Errorf("FullName() without a full name = %v, want ErrMalformedPayload", err)
	}
}

// TestPersonalRecordsEscapeTheDisplayNameIntoOneSegment is the path-safety test
// for a read keyed by identity material.
func TestPersonalRecordsEscapeTheDisplayNameIntoOneSegment(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(personalRecordsPath(),
		testkit.JSON(http.StatusOK, personalRecordsBody))
	h := newHarness(t, script, client.Limits{})

	records, err := newProfile(t, h).PersonalRecords(t.Context(), h.session, mustDisplayName(t))
	if err != nil {
		t.Fatalf("PersonalRecords() = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("%d records, want 1", len(records))
	}
	if value, ok := records[0].Value.Float64(); !ok || value != 1234.5 {
		t.Errorf("Value = %v/%v, want 1234.5", value, ok)
	}

	path := h.server.Requests()[0].Path
	if !strings.HasPrefix(path, client.PathPersonalRecords+"/") {
		t.Errorf("path = %q, want the display name as the last segment", path)
	}
	if strings.Contains(strings.TrimPrefix(path, client.PathPersonalRecords+"/"), "/") {
		t.Errorf("path = %q, want exactly one escaped display-name segment", path)
	}
}

// TestPersonalRecordsRefuseAnUnsetDisplayName reproduces upstream's
// _require_display_name: interpolating an unset name into the path yields a 403,
// so it is refused before dispatch.
func TestPersonalRecordsRefuseAnUnsetDisplayName(t *testing.T) {
	t.Parallel()

	h := newHarness(t, testkit.NewScript(), client.Limits{})
	if _, err := newProfile(t, h).PersonalRecords(t.Context(), h.session,
		client.DisplayName{}); !errors.Is(err, client.ErrValidation) {
		t.Fatalf("PersonalRecords() = %v, want ErrValidation", err)
	}
	if got := len(h.server.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

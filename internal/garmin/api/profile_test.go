package api_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

const socialProfileBody = `{"profileId":900001,"displayName":"fake-tester","fullName":"Fake Tester",` +
	`"location":"Somewhere","userLevel":5,"garminGUID":"synthetic-guid","aFieldWeDoNotKnow":{"x":1}}`

const userSettingsBody = `{"id":900001,"userData":{"measurementSystem":"metric","birthDate":"1990-01-01",` +
	`"weight":72000.0,"handedness":"RIGHT","futureField":[1,2]}}`

func newProfile(t *testing.T, h harness) *api.Profile {
	t.Helper()

	profile, err := api.NewProfile(h.rc)
	if err != nil {
		t.Fatalf("NewProfile() = %v", err)
	}
	return profile
}

func TestProfileSocialDecodesAFlatObject(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathSocialProfile,
		testkit.JSON(http.StatusOK, socialProfileBody))
	h := newHarness(t, script, client.Limits{})

	got, err := newProfile(t, h).Social(t.Context(), h.session)
	if err != nil {
		t.Fatalf("Social() = %v", err)
	}

	if got.DisplayName == nil || *got.DisplayName != fakeDisplayName {
		t.Errorf("DisplayName = %v, want %q", got.DisplayName, fakeDisplayName)
	}
	if got.ProfileID == nil || *got.ProfileID != 900001 {
		t.Errorf("ProfileID = %v, want 900001", got.ProfileID)
	}
	if got.FullName == nil || *got.FullName != fakeFullName {
		t.Errorf("FullName = %v, want the decoded value", got.FullName)
	}
	if got.Payload().Len() != len(socialProfileBody) {
		t.Errorf("the raw payload was not retained: Len() = %d", got.Payload().Len())
	}
	if got.Payload().Endpoint() != client.EndpointSocialProfile {
		t.Errorf("payload endpoint = %q, want the profile label", got.Payload().Endpoint())
	}
}

func TestProfileDisplayNameIsValidatedForPathUse(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathSocialProfile,
		testkit.JSON(http.StatusOK, socialProfileBody))
	h := newHarness(t, script, client.Limits{})

	name, err := newProfile(t, h).DisplayName(t.Context(), h.session)
	if err != nil {
		t.Fatalf("DisplayName() = %v", err)
	}
	if name.Value() != fakeDisplayName {
		t.Errorf("Value() = %q, want %q", name.Value(), fakeDisplayName)
	}
}

func TestProfileDisplayNameRefusesAnUnusableName(t *testing.T) {
	t.Parallel()

	// Source: _require_display_name, which refuses an empty display name because
	// "None" interpolated into a URL path yields a 403, and refuses anything that
	// could inject a path separator.
	cases := map[string]string{
		"absent":    `{"profileId":900001}`,
		"empty":     `{"profileId":900001,"displayName":""}`,
		"separator": `{"profileId":900001,"displayName":"a/../b"}`,
	}

	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			script := testkit.NewScript().With(client.PathSocialProfile, testkit.JSON(http.StatusOK, body))
			h := newHarness(t, script, client.Limits{})

			_, err := newProfile(t, h).DisplayName(t.Context(), h.session)
			if !errors.Is(err, client.ErrValidation) {
				t.Errorf("DisplayName() = %v, want a validation error", err)
			}
			if _, ok := errors.AsType[*client.APIError](err); !ok {
				t.Error("the failure is not an *APIError")
			}
		})
	}
}

func TestProfileSettingsDecodesNestedUserData(t *testing.T) {
	t.Parallel()

	script := testkit.NewScript().With(client.PathUserSettings,
		testkit.JSON(http.StatusOK, userSettingsBody))
	h := newHarness(t, script, client.Limits{})

	got, err := newProfile(t, h).Settings(t.Context(), h.session)
	if err != nil {
		t.Fatalf("Settings() = %v", err)
	}
	if got.UserData == nil {
		t.Fatal("UserData = nil, want the nested object")
	}
	if system, ok := got.UserData.MeasurementSystem.Value(); !ok || system != "metric" {
		t.Errorf("MeasurementSystem = %q/%v, want metric", system, ok)
	}
	if weight, ok := got.UserData.Weight.Float64(); !ok || weight != 72000 {
		t.Errorf("Weight = %v/%v, want 72000", weight, ok)
	}
	if got.UserData.BirthDate == nil || *got.UserData.BirthDate != "1990-01-01" {
		t.Errorf("BirthDate = %v, want the decoded value", got.UserData.BirthDate)
	}
}

func TestProfileMapsFailuresOntoAPIError(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		behavior testkit.Behavior
		sentinel error
	}{
		"rejected session": {testkit.JSON(http.StatusUnauthorized, `{"message":"no"}`), client.ErrAuthentication},
		"rate limited":     {testkit.RateLimited(2), client.ErrRateLimited},
		"missing":          {testkit.JSON(http.StatusNotFound, `{"message":"gone"}`), client.ErrNotFound},
		"garbage":          {testkit.JSON(http.StatusOK, `{"profileId":`), client.ErrMalformedPayload},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			script := testkit.NewScript().With(client.PathSocialProfile, tc.behavior)
			h := newHarness(t, script, client.Limits{MaxAttempts: 1})

			_, err := newProfile(t, h).Social(t.Context(), h.session)
			if !errors.Is(err, tc.sentinel) {
				t.Fatalf("Social() = %v, want %v", err, tc.sentinel)
			}
			var apiErr *client.APIError
			if !errors.As(err, &apiErr) {
				t.Fatal("the failure is not an *APIError")
			}
			if apiErr.Op != client.OpGetSocialProfile {
				t.Errorf("Op = %q, want %q", apiErr.Op, client.OpGetSocialProfile)
			}
		})
	}
}

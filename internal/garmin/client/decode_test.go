package client_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// tolerantProfile is the shape a domain model takes: optional pointers for fields
// Garmin may omit, and a RawMessage where the shape varies.
type tolerantProfile struct {
	DisplayName *string         `json:"displayName"`
	ProfileID   *int64          `json:"profileId"`
	Location    *string         `json:"location"`
	Extensions  json.RawMessage `json:"userProfileExtensions"`
}

func decodePayload(t *testing.T, body string, out any) error {
	t.Helper()

	caller := &stubCaller{outcomes: []stubOutcome{{
		status: http.StatusOK,
		header: jsonHeader(),
		body:   []byte(body),
	}}}
	payload, err := newTestClient(t, client.Limits{}).Do(t.Context(), mustSession(t, caller), profileRequest())
	if err != nil {
		t.Fatalf("Do() = %v", err)
	}
	return client.DecodeJSON(payload, out)
}

func TestDecodeJSONIgnoresUnknownFieldsAndKeepsOptionalOnesNil(t *testing.T) {
	t.Parallel()

	body := `{"displayName":"fake-tester","profileId":900001,` +
		`"unknownField":{"nested":true},"userProfileExtensions":[1,2,3]}`

	var profile tolerantProfile
	if err := decodePayload(t, body, &profile); err != nil {
		t.Fatalf("DecodeJSON() = %v, want an unknown field to be ignored", err)
	}
	if profile.DisplayName == nil || *profile.DisplayName != fakeDisplayName {
		t.Errorf("DisplayName = %v, want the decoded value", profile.DisplayName)
	}
	if profile.ProfileID == nil || *profile.ProfileID != 900001 {
		t.Errorf("ProfileID = %v, want 900001", profile.ProfileID)
	}
	if profile.Location != nil {
		t.Errorf("Location = %v, want nil for an absent field", *profile.Location)
	}
	if string(profile.Extensions) != "[1,2,3]" {
		t.Errorf("Extensions = %q, want the raw shape retained", string(profile.Extensions))
	}
}

func TestDecodeJSONReportsMalformedPayloadWithoutEchoingIt(t *testing.T) {
	t.Parallel()

	const secret = "SENTINEL-HEALTH-58bpm"
	body := `{"displayName":"` + secret + `"` // truncated on purpose

	var profile tolerantProfile
	err := decodePayload(t, body, &profile)
	if !errors.Is(err, client.ErrMalformedPayload) {
		t.Fatalf("DecodeJSON() = %v, want ErrMalformedPayload", err)
	}

	var apiErr *client.APIError
	if !errors.As(err, &apiErr) {
		t.Fatal("the failure is not an *APIError")
	}
	if apiErr.Endpoint != client.EndpointSocialProfile || apiErr.Op != client.OpGetSocialProfile {
		t.Errorf("labels = %q/%q, want the request's labels", apiErr.Op, apiErr.Endpoint)
	}
	if got := err.Error(); strings.Contains(got, secret) {
		t.Errorf("DecodeJSON() = %q leaks the payload it could not parse", got)
	}
}

func TestDecodeJSONLeavesTheTargetUntouchedForNoContent(t *testing.T) {
	t.Parallel()

	caller := &stubCaller{outcomes: []stubOutcome{{status: http.StatusNoContent}}}
	payload, err := newTestClient(t, client.Limits{}).Do(t.Context(), mustSession(t, caller), profileRequest())
	if err != nil {
		t.Fatalf("Do() = %v", err)
	}

	profile := tolerantProfile{}
	if err := client.DecodeJSON(payload, &profile); err != nil {
		t.Fatalf("DecodeJSON() = %v, want nil for a normalized 204", err)
	}
	if profile.DisplayName != nil {
		t.Error("a 204 must leave the target untouched")
	}
}

func TestNumberToleratesEveryShapeGarminUses(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		json    string
		want    float64
		present bool
	}{
		"number":         {`5`, 5, true},
		"float":          {`58.5`, 58.5, true},
		"numeric string": {`"58"`, 58, true},
		"spaced string":  {`" 58 "`, 58, true},
		jsonNullLiteral:  {jsonNullLiteral, 0, false},
		"empty string":   {`""`, 0, false},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var number client.Number
			if err := json.Unmarshal([]byte(tc.json), &number); err != nil {
				t.Fatalf("Unmarshal(%s) = %v, want nil", tc.json, err)
			}
			got, ok := number.Float64()
			if ok != tc.present {
				t.Errorf("Float64() presence = %v, want %v", ok, tc.present)
			}
			if ok && got != tc.want {
				t.Errorf("Float64() = %v, want %v", got, tc.want)
			}
		})
	}

	var number client.Number
	if err := json.Unmarshal([]byte(`{"unexpected":true}`), &number); err == nil {
		t.Error("Unmarshal of an object into a Number = nil, want an error")
	}
}

// TestInt64ExactRefusesEveryIdentifierAFloatWouldInvent is the identifier half of the
// numeric decoder, and it is deliberately not the same question as Float64.
//
// Int64 answers from the parsed float64, which is the right type for a measurement and
// the wrong one for an identifier: it truncates, so an answer naming 123.9 becomes 123
// and compares equal to a request for workout 123; and above 2^53 it cannot hold two
// consecutive integers apart, so an identifier one away from the requested one compares
// equal to it. Both of those turn "the answer names a different object" into "the answer
// is the object you asked for", which is the check the workout update depends on.
func TestInt64ExactRefusesEveryIdentifierAFloatWouldInvent(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		json  string
		want  int64
		exact bool
	}{
		"an integer":                      {`123`, 123, true},
		"an integer as a numeric string":  {`"123"`, 123, true},
		"an integer above 2^53":           {`9007199254740993`, 9007199254740993, true},
		"an integer at the int64 ceiling": {`9223372036854775807`, 9223372036854775807, true},
		"a fractional value":              {`123.9`, 0, false},
		"a fractional value at .0":        {`123.0`, 0, false},
		"an exponent form":                {`1.23e2`, 0, false},
		"a value past the int64 range":    {`9223372036854775808`, 0, false},
		"an absent field":                 {jsonNullLiteral, 0, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var number client.Number
			if err := json.Unmarshal([]byte(tc.json), &number); err != nil {
				t.Fatalf("Unmarshal(%s) = %v, want nil", tc.json, err)
			}
			got, exact := number.Int64Exact()
			if exact != tc.exact {
				t.Fatalf("Int64Exact(%s) exactness = %v, want %v", tc.json, exact, tc.exact)
			}
			if exact && got != tc.want {
				t.Errorf("Int64Exact(%s) = %d, want %d", tc.json, got, tc.want)
			}
		})
	}

	// The two shapes a float64 cannot tell apart, stated as the difference between the
	// two accessors, so a regression that routed an identifier back through Int64 is
	// visible here rather than only at a call site.
	var fractional, big client.Number
	if err := json.Unmarshal([]byte(`123.9`), &fractional); err != nil {
		t.Fatalf("Unmarshal(123.9) = %v, want nil", err)
	}
	if truncated, _ := fractional.Int64(); truncated != 123 {
		t.Errorf("Int64(123.9) = %d, want the truncation this test exists to refuse", truncated)
	}
	if err := json.Unmarshal([]byte(`9007199254740993`), &big); err != nil {
		t.Fatalf("Unmarshal(2^53+1) = %v, want nil", err)
	}
	if rounded, _ := big.Int64(); rounded != 9007199254740992 {
		t.Errorf("Int64(2^53+1) = %d, want the rounding this test exists to refuse", rounded)
	}

	// A Number built in code carries no literal and is answered from the float, under
	// the same rule.
	if _, exact := client.NewNumber(123.9).Int64Exact(); exact {
		t.Error("Int64Exact() called a fractional in-code value an exact identifier")
	}
	if got, exact := client.NewNumber(123).Int64Exact(); !exact || got != 123 {
		t.Errorf("Int64Exact() = (%d, %v) for an in-code whole number, want (123, true)",
			got, exact)
	}
	if _, exact := client.NewNumber(1 << 54).Int64Exact(); exact {
		t.Error("Int64Exact() called an in-code value past 2^53 an exact identifier")
	}
}

func TestTextToleratesEveryShapeGarminUses(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		json    string
		want    string
		present bool
	}{
		"string":        {`"running"`, "running", true},
		"number":        {`12`, "12", true},
		"boolean":       {`true`, "true", true},
		jsonNullLiteral: {jsonNullLiteral, "", false},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var text client.Text
			if err := json.Unmarshal([]byte(tc.json), &text); err != nil {
				t.Fatalf("Unmarshal(%s) = %v, want nil", tc.json, err)
			}
			got, ok := text.Value()
			if ok != tc.present {
				t.Errorf("Value() presence = %v, want %v", ok, tc.present)
			}
			if got != tc.want {
				t.Errorf("Value() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestListDecodesASingleObjectAndAnArrayAlike(t *testing.T) {
	t.Parallel()

	type lap struct {
		Distance client.Number `json:"distance"`
	}

	cases := map[string]struct {
		json string
		want int
	}{
		"array":         {`[{"distance":400},{"distance":"800"}]`, 2},
		"single object": {`{"distance":400}`, 1},
		jsonNullLiteral: {jsonNullLiteral, 0},
		"empty array":   {`[]`, 0},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var list client.List[lap]
			if err := json.Unmarshal([]byte(tc.json), &list); err != nil {
				t.Fatalf("Unmarshal(%s) = %v, want nil", tc.json, err)
			}
			if got := len(list.Items()); got != tc.want {
				t.Errorf("len(Items()) = %d, want %d", got, tc.want)
			}
		})
	}
}

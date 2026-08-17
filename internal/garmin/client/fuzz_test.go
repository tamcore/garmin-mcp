package client_test

import (
	"math"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// Fuzz targets for the tolerant scalar decoders and the date parser: the
// request-shaping code that reads a Garmin field whose spelling drifts by
// device and locale, and the code that turns caller-supplied text into a URL
// path segment. None of these targets performs I/O.

// FuzzNumberUnmarshalJSON exercises the union decoder that accepts a number,
// a numeric string, null and an empty string for one Garmin field.
//
// The property is the decoder's own promise: a Number that reports IsSet must
// hand back a finite value from both Float64 and Int64Exact, and disagreeing
// with itself about presence, or handing back NaN/Inf as "set", is the defect
// this guards.
func FuzzNumberUnmarshalJSON(f *testing.F) {
	f.Add(`123.45`)
	f.Add(`"123.45"`)
	f.Add(`null`)
	f.Add(`""`)
	f.Add(`"not a number"`)
	f.Add(`1e400`)
	f.Fuzz(func(t *testing.T, data string) {
		var n client.Number
		if err := n.UnmarshalJSON([]byte(data)); err != nil {
			return
		}

		value, floatOK := n.Float64()
		if floatOK != n.IsSet() {
			t.Fatalf("Float64 presence %v disagrees with IsSet %v for %q", floatOK, n.IsSet(), data)
		}
		if n.IsSet() && (math.IsNaN(value) || math.IsInf(value, 0)) {
			t.Fatalf("IsSet true but Float64 is not finite (%v) for %q", value, data)
		}

		if exact, exactOK := n.Int64Exact(); exactOK && !n.IsSet() {
			t.Fatalf("Int64Exact reported %d as exact but IsSet is false for %q", exact, data)
		}

		_, _ = n.Int64()
	})
}

// FuzzTextUnmarshalJSON exercises the union decoder that accepts a string, a
// number, a boolean and null for one Garmin field.
//
// The property: a Text that decodes without error must never claim a value it
// cannot render back out as JSON, and Value's presence flag must agree with
// IsSet.
func FuzzTextUnmarshalJSON(f *testing.F) {
	f.Add(`"hello"`)
	f.Add(`123`)
	f.Add(`true`)
	f.Add(`null`)
	f.Add(`{}`)
	f.Fuzz(func(t *testing.T, data string) {
		var txt client.Text
		if err := txt.UnmarshalJSON([]byte(data)); err != nil {
			return
		}

		value, valueOK := txt.Value()
		if valueOK != txt.IsSet() {
			t.Fatalf("Value presence %v disagrees with IsSet %v for %q", valueOK, txt.IsSet(), data)
		}
		if _, err := txt.MarshalJSON(); err != nil {
			t.Fatalf("MarshalJSON failed for a successfully decoded Text %q (from %q): %v", value, data, err)
		}
	})
}

// FuzzParseDate exercises the strict YYYY-MM-DD calendar-date parser. Its
// round-trip check is the real guarantee: a date ParseDate accepts must render
// back to exactly the trimmed input, so an out-of-range day such as
// 2024-02-30 silently normalizing to 2024-03-01 is the defect this catches.
func FuzzParseDate(f *testing.F) {
	f.Add("2024-01-01")
	f.Add("2024-02-30")
	f.Add(" 2024-01-01 ")
	f.Add("2024-01-01T00:00:00Z")
	f.Add("")
	f.Fuzz(func(t *testing.T, value string) {
		date, err := client.ParseDate(value)
		if err != nil {
			return
		}
		trimmed := strings.TrimSpace(value)
		if got := date.String(); got != trimmed {
			t.Fatalf("ParseDate(%q) succeeded but round-trip mismatch: date.String() = %q", value, got)
		}
	})
}

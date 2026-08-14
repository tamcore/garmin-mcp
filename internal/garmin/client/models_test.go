package client_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

func TestParseDateAcceptsGarminCalendarDates(t *testing.T) {
	t.Parallel()

	date, err := client.ParseDate("2026-01-31")
	if err != nil {
		t.Fatalf("ParseDate() = %v, want nil", err)
	}
	if got := date.String(); got != testCalendarDate {
		t.Errorf("String() = %q, want %q", got, "2026-01-31")
	}
	if date.IsZero() {
		t.Error("IsZero() = true for a parsed date")
	}
	if got := date.Time().Location(); got != time.UTC {
		t.Errorf("Time() location = %v, want UTC so a calendar date has no local drift", got)
	}
}

func TestParseDateRejectsMalformedValues(t *testing.T) {
	t.Parallel()

	// Source: _validate_date_format, which requires a full YYYY-MM-DD match and a
	// real calendar date.
	for _, value := range []string{
		"", "2026-1-31", "31-01-2026", "2026-02-30", "2026-13-01",
		"2026-01-31T00:00:00Z", "../2026-01-31", "2026-01-31&x=1",
	} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()

			if _, err := client.ParseDate(value); !errors.Is(err, client.ErrValidation) {
				t.Errorf("ParseDate(%q) = %v, want a validation error", value, err)
			}
		})
	}
}

func TestParseDateToleratesSurroundingSpace(t *testing.T) {
	t.Parallel()

	// Source: _validate_date_format strips surrounding whitespace before matching.
	date, err := client.ParseDate("  2026-01-31  ")
	if err != nil {
		t.Fatalf("ParseDate() = %v, want nil", err)
	}
	if got := date.String(); got != testCalendarDate {
		t.Errorf("String() = %q, want %q", got, "2026-01-31")
	}
}

func TestNewDateRangeRequiresOrderedDates(t *testing.T) {
	t.Parallel()

	start := mustDate(t, "2026-01-01")
	end := mustDate(t, "2026-01-31")

	span, err := client.NewDateRange(start, end)
	if err != nil {
		t.Fatalf("NewDateRange() = %v, want nil", err)
	}
	if got := span.Days(); got != 31 {
		t.Errorf("Days() = %d, want 31 for an inclusive January window", got)
	}

	if _, err := client.NewDateRange(end, start); !errors.Is(err, client.ErrValidation) {
		t.Errorf("a reversed range must be rejected, got %v", err)
	}
	if _, err := client.NewDateRange(client.Date{}, end); !errors.Is(err, client.ErrValidation) {
		t.Errorf("an unset start must be rejected, got %v", err)
	}
}

func TestLimitsBoundDateRange(t *testing.T) {
	t.Parallel()

	limits := client.Limits{MaxDateRangeDays: 7}
	within, err := client.NewDateRange(mustDate(t, "2026-01-01"), mustDate(t, "2026-01-07"))
	if err != nil {
		t.Fatalf("NewDateRange() = %v", err)
	}
	if err := limits.ValidateDateRange(within); err != nil {
		t.Errorf("ValidateDateRange() = %v, want nil for a 7-day window", err)
	}

	beyond, err := client.NewDateRange(mustDate(t, "2026-01-01"), mustDate(t, "2026-01-08"))
	if err != nil {
		t.Fatalf("NewDateRange() = %v", err)
	}
	if err := limits.ValidateDateRange(beyond); !errors.Is(err, client.ErrValidation) {
		t.Errorf("ValidateDateRange() = %v, want a validation error for an 8-day window", err)
	}
}

func TestNewPageValidatesAndAdvances(t *testing.T) {
	t.Parallel()

	page, err := client.NewPage(0, 20)
	if err != nil {
		t.Fatalf("NewPage() = %v, want nil", err)
	}
	if page.Start() != 0 || page.Limit() != 20 {
		t.Errorf("NewPage(0, 20) = (%d, %d), want (0, 20)", page.Start(), page.Limit())
	}
	if next := page.Next(); next.Start() != 20 || next.Limit() != 20 {
		t.Errorf("Next() = (%d, %d), want (20, 20)", next.Start(), next.Limit())
	}

	// Source: _validate_non_negative_integer(start), _validate_positive_integer(limit)
	// and the MAX_ACTIVITY_LIMIT ceiling in get_activities.
	for name, args := range map[string][2]int{
		"negative start": {-1, 20},
		"zero limit":     {0, 0},
		"negative limit": {0, -5},
		"limit over cap": {0, client.MaxPageSizeCap + 1},
		"start over cap": {client.MaxPageStartCap + 1, 20},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := client.NewPage(args[0], args[1]); !errors.Is(err, client.ErrValidation) {
				t.Errorf("NewPage(%d, %d) = %v, want a validation error", args[0], args[1], err)
			}
		})
	}
}

func TestLimitsBoundPageSize(t *testing.T) {
	t.Parallel()

	limits := client.Limits{MaxPageSize: 20}
	ok, err := client.NewPage(0, 20)
	if err != nil {
		t.Fatalf("NewPage() = %v", err)
	}
	if err := limits.ValidatePage(ok); err != nil {
		t.Errorf("ValidatePage() = %v, want nil", err)
	}

	tooBig, err := client.NewPage(0, 21)
	if err != nil {
		t.Fatalf("NewPage() = %v", err)
	}
	if err := limits.ValidatePage(tooBig); !errors.Is(err, client.ErrValidation) {
		t.Errorf("ValidatePage() = %v, want a validation error", err)
	}
}

func TestParseIDRequiresAPositiveInteger(t *testing.T) {
	t.Parallel()

	// Source: _validate_positive_integer(int(activity_id), "activity_id").
	id, err := client.ParseID("18446744")
	if err != nil {
		t.Fatalf("ParseID() = %v, want nil", err)
	}
	if id.String() != "18446744" || id.Int64() != 18446744 {
		t.Errorf("ParseID() = %q/%d, want 18446744", id.String(), id.Int64())
	}

	for _, value := range []string{"", "0", "-1", "1.5", "12a", " 12", "0x0c", "٣"} {
		if _, err := client.ParseID(value); !errors.Is(err, client.ErrValidation) {
			t.Errorf("ParseID(%q) = %v, want a validation error", value, err)
		}
	}
	if _, err := client.NewID(0); !errors.Is(err, client.ErrValidation) {
		t.Errorf("NewID(0) = %v, want a validation error", err)
	}
}

func TestParseDisplayNameRejectsHostileValues(t *testing.T) {
	t.Parallel()

	// Source: _require_display_name, which refuses an empty display name and
	// percent-encodes the value before it enters a URL path.
	for _, value := range []string{"", "   ", "..", ".", "a/b", "a\\b", "a\x00b", "a\nb"} {
		if _, err := client.ParseDisplayName(value); !errors.Is(err, client.ErrValidation) {
			t.Errorf("ParseDisplayName(%q) = %v, want a validation error", value, err)
		}
	}

	name, err := client.ParseDisplayName("fake.tester-42")
	if err != nil {
		t.Fatalf("ParseDisplayName() = %v, want nil", err)
	}
	if got := name.Value(); got != "fake.tester-42" {
		t.Errorf("Value() = %q, want the display name the accessor exists to return", got)
	}
}

// TestDisplayNameIsNotPrintable proves identity material follows the repository
// redaction convention, including through a method-stripping alias.
func TestDisplayNameIsNotPrintable(t *testing.T) {
	t.Parallel()

	const sentinel = "SENTINEL-DISPLAY-NAME-7c31"
	name, err := client.ParseDisplayName(sentinel)
	if err != nil {
		t.Fatalf("ParseDisplayName() = %v", err)
	}

	type stripped client.DisplayName
	values := map[string]any{
		"value":       name,
		"pointer":     &name,
		"stripped":    stripped(name),
		"in a struct": struct{ N stripped }{stripped(name)},
		"in a slice":  []stripped{stripped(name)},
	}
	needles := []string{sentinel, decimalBytesOf(sentinel)}

	for label, value := range values {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%d"} {
			rendered := fmt.Sprintf(verb, value)
			for _, needle := range needles {
				if strings.Contains(rendered, needle) {
					t.Errorf("%s rendered with %s leaks the display name", label, verb)
				}
			}
		}
	}
}

func TestDisplayNameRedactsItsRenderedForms(t *testing.T) {
	t.Parallel()

	const sentinel = "SENTINEL-DISPLAY-NAME-9d02"
	name, err := client.ParseDisplayName(sentinel)
	if err != nil {
		t.Fatalf("ParseDisplayName() = %v", err)
	}

	marshaled, err := json.Marshal(name)
	if err != nil {
		t.Fatalf("json.Marshal() = %v", err)
	}

	var logged strings.Builder
	slog.New(slog.NewTextHandler(&logged, nil)).Info("profile", "displayName", name)

	rendered := map[string]string{
		"String":      name.String(),
		"GoString":    name.GoString(),
		"MarshalJSON": string(marshaled),
		"LogValue":    logged.String(),
	}
	for label, value := range rendered {
		if strings.Contains(value, sentinel) {
			t.Errorf("%s = %q leaks the display name", label, value)
		}
		if value == "" {
			t.Errorf("%s is empty, want a presence report", label)
		}
	}

	zero := client.DisplayName{}
	if strings.Contains(zero.String(), "set") && !strings.Contains(zero.String(), "unset") {
		t.Errorf("the zero DisplayName renders %q, want it to report absence", zero.String())
	}
}

func mustDate(t *testing.T, value string) client.Date {
	t.Helper()

	date, err := client.ParseDate(value)
	if err != nil {
		t.Fatalf("ParseDate(%q) = %v", value, err)
	}
	return date
}

// decimalBytesOf renders s the way fmt prints a []byte, because leaked material
// shows up as decimal byte values and stays fully recoverable.
func decimalBytesOf(s string) string {
	parts := make([]string, 0, len(s))
	for _, b := range []byte(s) {
		parts = append(parts, fmt.Sprintf("%d", b))
	}
	return strings.Join(parts, " ")
}

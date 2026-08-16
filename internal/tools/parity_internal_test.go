// The activity-management parity contract tests. The tools themselves are driven by
// the single in-package harness in harness_internal_test.go, which builds the real
// registrar from register.go; the list below stays because these three tests assert
// over a named slice rather than over the whole registered surface.
package tools

import (
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// parityRegistrations names the activity-management parity slice. It is a subset of
// the list register.go takes, so a drifting contract name fails these tests before it
// can reach the wiring.
func parityRegistrations() []registration {
	return []registration{
		{countActivitiesContract, registerCountActivities},
		{getActivitiesForDateContract, registerGetActivitiesForDate},
		{getActivityContract, registerGetActivity},
		{getActivityGearContract, registerGetActivityGear},
		{getActivityTypesContract, registerGetActivityTypes},
	}
}

// Synthetic identities. No fixture in this file is a recording of a real account.
const (
	parityActivityID = "987654321"
	parityDate       = "2026-01-31"
)

// Argument and result keys the parity tests assert on, named once so a rename shows
// up in one place. argActivityID lives in args.go: non-test code needs it too.
const (
	argDate        = "date"
	keyDisplayName = "display_name"
	typeKeyRunning = "running"
)

func parityActivityPath() string {
	return client.PathActivityPrefix + "/" + parityActivityID
}

func parityForDatePath() string {
	return client.PathActivitiesForDatePrefix + "/" + parityDate
}

// TestParityToolsDeclareTheReadOnlyContract covers all five at once: the upstream
// name, the read-only tier, the manifest's sensitivity category, and all four
// annotation hints. A wrong hint is not cosmetic — a client decides whether to
// prompt its user from it.
func TestParityToolsDeclareTheReadOnlyContract(t *testing.T) {
	t.Parallel()

	wantCategory := map[string]string{
		ToolCountActivities:      categoryOrdinary,
		ToolGetActivitiesForDate: categoryLocation,
		ToolGetActivity:          categoryLocation,
		ToolGetActivityGear:      categoryDevice,
		ToolGetActivityTypes:     categoryOrdinary,
	}
	for _, entry := range parityRegistrations() {
		spec := entry.contract().Spec
		t.Run(spec.Name, func(t *testing.T) {
			t.Parallel()

			if spec.Tier != policy.TierReadOnly {
				t.Errorf("tier = %v, want the read-only tier", spec.Tier)
			}
			if spec.Category != wantCategory[spec.Name] {
				t.Errorf("category = %q, want %q", spec.Category, wantCategory[spec.Name])
			}
			hints := spec.Annotations
			if !hints.ReadOnly || hints.Destructive || !hints.Idempotent || !hints.OpenWorld {
				t.Errorf("annotations = %+v, want read-only, idempotent, open-world", hints)
			}
			if spec.Description == "" || spec.Title == "" {
				t.Error("the tool declares no title or no description")
			}
		})
	}
}

// TestParityToolsAcceptNoAccountSelector is the principal rule, asserted over the
// whole slice: the account comes from the request context and from nowhere else.
func TestParityToolsAcceptNoAccountSelector(t *testing.T) {
	t.Parallel()

	forbidden := []string{"user_id", keyEmail, keyDisplayName, "token", "token_path", "path"}
	for _, entry := range parityRegistrations() {
		contract := entry.contract()
		for _, property := range contract.Schema.Properties() {
			for _, name := range forbidden {
				if property.Name == name {
					t.Errorf("%s declares the argument %q", contract.Spec.Name, name)
				}
			}
		}
	}
}

// TestParityResultsLogTheirShapeAndNotTheirContent is the log-redaction test. A
// result that reaches a log sink by accident must reveal counts and presence flags
// only, never a measurement, a name or an identifier.
func TestParityResultsLogTheirShapeAndNotTheirContent(t *testing.T) {
	t.Parallel()

	name := "Synthetic run"
	measurement := 987654321.0
	values := []slog.LogValuer{
		ActivityCount{TotalActivities: 4242, Note: countActivitiesNote},
		ActivityDetail{ActivityID: 987654321, Name: &name, ActivityType: &name},
		ActivityGearList{ActivityID: 987654321, Gear: []ActivityGear{{DisplayName: &name}}, Count: 1},
		ActivityTypeList{ActivityTypes: []ActivityTypeEntry{{TypeKey: &name}}, Count: 1},
		DailyActivityList{
			Date:       parityDate,
			Activities: []DailyActivity{{Name: &name, DistanceMeters: &measurement}},
			Count:      1,
		},
	}
	for _, value := range values {
		rendered := fmt.Sprintf("%v", value.LogValue())
		for _, forbidden := range []string{name, "987654321", "4242", parityDate} {
			if strings.Contains(rendered, forbidden) {
				t.Errorf("%T logged %q, which its LogValue must withhold", value, forbidden)
			}
		}
		if !strings.Contains(rendered, "model=") {
			t.Errorf("%T logged %q, which names no model", value, rendered)
		}
	}
}

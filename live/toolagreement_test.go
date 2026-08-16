//go:build garminlive

package live

import (
	"math"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/tools"
)

// Result keys named more than once, so a rename shows up in one place.
const keyDistanceMeters = "distance_meters"

// exactAgreement is the tolerance between a tool result and the domain client that
// backs it. Both read the same Garmin document through the same decoders, so any
// difference is a dropped, transposed or wrongly scaled field rather than a rounding.
const exactAgreement = 1e-9

// renderedUnit is the tolerance between a computed FIT figure and the one the tool
// renders. The tool rounds for the wire, so the two may differ by less than a unit
// and by no more.
const renderedUnit = 1.0

// TestToolResultsAgreeWithTheAPILayer drives registered read-only tools and compares
// each result with an independent read through the domain client the tool is built on.
//
// This is the layer a fixture covers weakly: a unit test scripts one response and
// asserts the mapping the same test declared, so a field this server transposes on
// the way out stays invisible. Here the two sides are two separate reads of the live
// account, and only the mapping is shared.
func TestToolResultsAgreeWithTheAPILayer(t *testing.T) {
	e := liveEnv(t)

	t.Run(tools.ToolGetFullName, func(t *testing.T) { e.assertFullNameAgrees(t) })
	t.Run(tools.ToolGetDevices, func(t *testing.T) { e.assertDevicesAgree(t) })
	t.Run(tools.ToolGetActivity, func(t *testing.T) { e.assertActivityAgrees(t) })
	t.Run(tools.ToolGetActivityFITData, func(t *testing.T) { e.assertFITDataAgrees(t) })
}

// assertFullNameAgrees compares the profile tool with the profile client.
func (e *env) assertFullNameAgrees(t *testing.T) {
	t.Helper()

	direct, err := e.profile.FullName(t.Context(), e.session)
	if err != nil {
		t.Fatalf("reading the full name through the profile client: %v", err)
	}
	result := e.call(t, tools.ToolGetFullName, nil)

	viaTool, ok := result["full_name"].(string)
	if !ok {
		t.Fatalf("%s returned no full_name string", tools.ToolGetFullName)
	}
	if viaTool != direct {
		t.Error("full_name from the tool differs from the profile client's own reading")
	}
}

// assertDevicesAgree compares the device tool with the device client.
func (e *env) assertDevicesAgree(t *testing.T) {
	t.Helper()

	direct, err := e.devices.List(t.Context(), e.session)
	if err != nil {
		t.Fatalf("listing devices through the device client: %v", err)
	}
	result := e.call(t, tools.ToolGetDevices, nil)

	listed, _ := result["devices"].([]any)
	count, hasCount := result["count"].(float64)
	if !hasCount {
		t.Fatalf("%s returned no count", tools.ToolGetDevices)
	}
	if int(count) != len(listed) {
		t.Errorf("%s reports %d devices in count and %d in the list",
			tools.ToolGetDevices, int(count), len(listed))
	}
	if truncated, _ := result["truncated"].(bool); !truncated && len(listed) != len(direct) {
		t.Errorf("%s returned %d devices and the device client returned %d",
			tools.ToolGetDevices, len(listed), len(direct))
	}
}

// activityFigure is one figure compared between the single-activity tool and the
// activity record the detail client reads.
type activityFigure struct {
	field string
	path  []string
	rest  client.Number
}

// assertActivityAgrees compares the single-activity tool with the activity record the
// detail client reads.
func (e *env) assertActivityAgrees(t *testing.T) {
	t.Helper()

	a := analysedActivity(t)
	figures, err := e.summaryFiguresOf(t.Context(), a.id)
	if err != nil {
		t.Fatalf("reading the activity summary: %v", err)
	}
	result := e.call(t, tools.ToolGetActivity, map[string]any{argActivityID: a.id.String()})

	if id, ok := result[argActivityID].(float64); !ok || int64(id) != a.id.Int64() {
		t.Errorf("%s reported a different activity than the one asked for", tools.ToolGetActivity)
	}

	compared := 0
	for _, c := range []activityFigure{
		{keyDistanceMeters, []string{keyDistance, keyDistanceMeters}, figures.Distance},
		{"elapsed_seconds", []string{"timing", "elapsed_seconds"}, figures.ElapsedDuration},
		{"average_bpm", []string{keyHeartRate, "average_bpm"}, figures.AverageHR},
		{"max_bpm", []string{"heart_rate", "max_bpm"}, figures.MaxHR},
		{"calories", []string{"energy", "calories"}, figures.Calories},
		{"gain_meters", []string{"elevation", "gain_meters"}, figures.ElevationGain},
	} {
		rest, present := c.rest.Float64()
		viaTool, rendered := nested(result, c.path...)
		if !present || !rendered {
			continue
		}
		compared++
		if delta := math.Abs(viaTool - rest); delta > exactAgreement {
			t.Errorf("%s from %s disagrees with the activity record by %.3f%%",
				c.field, tools.ToolGetActivity, 100*relative(delta, rest))
		}
	}
	if compared == 0 {
		t.Fatalf("%s carried none of the compared figures, so nothing was proven",
			tools.ToolGetActivity)
	}
}

// sessionFigure is one computed session figure compared with the one the FIT tool
// renders.
type sessionFigure struct {
	field string
	key   string
	value float64
	ok    bool
}

// assertFITDataAgrees compares the FIT analysis tool with the same file decoded and
// analysed directly through the api package.
func (e *env) assertFITDataAgrees(t *testing.T) {
	t.Helper()

	a := analysedActivity(t)
	result := e.call(t, tools.ToolGetActivityFITData, map[string]any{argActivityID: a.id.String()})

	sessions, _ := result["sessions"].([]any)
	if len(sessions) != len(a.summary.Sessions) {
		t.Fatalf("%s reported %d sessions and the api layer computed %d",
			tools.ToolGetActivityFITData, len(sessions), len(a.summary.Sessions))
	}
	if size, ok := result["file_bytes"].(float64); !ok || int(size) != a.fileSize {
		t.Errorf("%s reported a different downloaded size than the direct download",
			tools.ToolGetActivityFITData)
	}

	first, ok := sessions[0].(map[string]any)
	if !ok {
		t.Fatalf("%s returned a session that is not an object", tools.ToolGetActivityFITData)
	}
	direct := a.summary.Sessions[0]
	for _, c := range []sessionFigure{
		{"distance", keyDistanceMeters, direct.Distance.Value, direct.Distance.OK},
		{"ascent", "ascent_meters", direct.Ascent.Value, direct.Ascent.OK},
		{"average heart rate", "average_heart_rate", direct.AvgHeartRate.Value, direct.AvgHeartRate.OK},
		{"duration", "duration_seconds", direct.Seconds.Value, direct.Seconds.OK},
	} {
		e.compareSessionFigure(t, first, c)
	}
}

// compareSessionFigure checks one rendered session figure against the computed one.
func (e *env) compareSessionFigure(t *testing.T, session map[string]any, c sessionFigure) {
	t.Helper()

	if !c.ok {
		return
	}
	viaTool, rendered := nested(session, c.key)
	if !rendered {
		t.Errorf("%s dropped the session %s the api layer computed",
			tools.ToolGetActivityFITData, c.field)
		return
	}
	if delta := math.Abs(viaTool - c.value); delta >= renderedUnit {
		t.Errorf("session %s from %s disagrees with the api layer by %.3f%%",
			c.field, tools.ToolGetActivityFITData, 100*relative(delta, c.value))
	}
}

// nested reads a float from a path of object keys, reporting whether the path exists.
func nested(document map[string]any, path ...string) (float64, bool) {
	var current any = document
	for _, key := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return 0, false
		}
		current, ok = object[key]
		if !ok {
			return 0, false
		}
	}
	value, ok := current.(float64)
	return value, ok
}

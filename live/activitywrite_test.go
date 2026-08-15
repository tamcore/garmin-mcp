//go:build garminlive

package live

import (
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/tools"
)

// The manual activity the lifecycle creates. Every figure is arbitrary and bounded,
// and none of them describes the account.
const (
	manualTypeKey    = "yoga"
	manualDuration   = 30
	manualStartClock = "09:00"
	manualTimeZone   = "UTC"

	// manualFeel and manualEffort are the two subjective ratings. Garmin stores the
	// feel as written and scales the effort by ten, and both relationships are
	// asserted rather than assumed.
	manualFeel   = 50
	manualEffort = 5

	// effortScale is the factor Garmin stores a perceived effort with. It is the api
	// package's own documented behaviour, checked here against the real service.
	effortScale = 10
)

// Result and argument field names used by the activity writes.
const (
	keyActivityName = "activity_name"
	keyActivityType = "activity_type"
	keyEventType    = "event_type"
	keyDescription  = "description"
	keyTypeKey      = "type_key"
	keyFeedbackFeel = "workout_feel"
	keyFeedbackRPE  = "workout_rpe"
	keyTimeZone     = "time_zone"
)

// TestLiveManualActivityLifecycle drives one manual activity from creation to
// removal, exercising every per-activity metadata write on the way.
//
// The six metadata tools are the ones a live run would otherwise never touch: they
// are all write-tier, they all PUT to the same per-activity path, and a fixture
// cannot tell whether Garmin actually stored what was sent. Here each one is written
// and then read back from the activity record.
//
// The subject is an activity this suite created, and the guard refuses the write if
// it is not, so no metadata write can reach the maintainer's own activity.
func TestLiveManualActivityLifecycle(t *testing.T) {
	w := liveWriteEnv(t)

	name := suiteName("activity")
	date := time.Now().UTC().AddDate(0, 0, -1).Format(time.DateOnly)
	created := w.call(t, tools.ToolCreateManualActivity, map[string]any{
		keyTypeKey:         manualTypeKey,
		argDate:            date,
		"duration_minutes": manualDuration,
		"start_time":       manualStartClock,
		keyActivityName:    name,
		keyTimeZone:        manualTimeZone,
	})
	id := identifier(t, created, tools.ToolCreateManualActivity, argActivityID)
	w.keepClean(t, kindActivity, id)

	if !w.owned.owns(kindActivity, id) {
		t.Fatal("the write guard did not learn the created activity from Garmin's own " +
			"response, so every metadata write against it would be refused")
	}

	detail := w.readActivity(t, id)
	assertSuiteValue(t, tools.ToolGetActivity, keyActivityName, name, detail)
	if stored, _ := detail[keyActivityType].(string); stored != manualTypeKey {
		t.Errorf("%s stored a different activity type than the one requested",
			tools.ToolCreateManualActivity)
	}

	written := w.writeEveryMetadataField(t, id, detail)
	w.assertMetadataReadsBack(t, id, written)

	w.deleteViaTool(t, tools.ToolDeleteActivity, argActivityID, kindActivity, id)
	w.assertActivityIsGone(t, id)
}

// metadataWritten is what the six metadata writes sent, so the read-back compares
// against those values rather than against a fixture.
type metadataWritten struct {
	name        string
	typeKey     string
	eventType   string
	description string
}

// writeEveryMetadataField drives all six per-activity write tools.
func (w *writeEnv) writeEveryMetadataField(
	t *testing.T, id int64, detail map[string]any,
) metadataWritten {
	t.Helper()

	current, _ := detail[keyActivityType].(string)
	written := metadataWritten{
		name:        suiteName("activity-renamed"),
		typeKey:     w.otherActivityType(t, current),
		eventType:   firstEventType(t),
		description: suiteName("description"),
	}
	activity := map[string]any{argActivityID: id}

	w.call(t, tools.ToolSetActivityName, merged(activity, keyActivityName, written.name))
	w.call(t, tools.ToolSetActivityType, merged(activity, keyTypeKey, written.typeKey))
	w.call(t, tools.ToolSetActivityEventType, merged(activity, keyEventType, written.eventType))
	w.call(t, tools.ToolSetActivityDescription,
		merged(activity, keyDescription, written.description))
	w.call(t, tools.ToolSetActivityFeel, merged(activity, "feel", manualFeel))
	w.call(t, tools.ToolSetPerceivedEffort, merged(activity, "rpe", manualEffort))

	return written
}

// assertMetadataReadsBack compares every written field with the activity record.
//
// The two ratings are checked against the relationship the api package documents:
// the feel is stored as written and the effort is scaled by ten. A Garmin change to
// either scale breaks here and nowhere else, because every fixture agrees with the
// scale the test that wrote it declared.
func (w *writeEnv) assertMetadataReadsBack(t *testing.T, id int64, written metadataWritten) {
	t.Helper()

	detail := w.readActivity(t, id)
	assertSuiteValue(t, tools.ToolSetActivityName, keyActivityName, written.name, detail)
	assertSuiteValueContains(t, tools.ToolSetActivityDescription,
		keyDescription, written.description, detail)

	if stored, _ := detail[keyActivityType].(string); stored != written.typeKey {
		t.Errorf("%s did not store the activity type it was given", tools.ToolSetActivityType)
	}
	if stored, _ := detail[keyEventType].(string); stored != written.eventType {
		t.Errorf("%s did not store the event type it was given", tools.ToolSetActivityEventType)
	}

	w.assertRating(t, detail, tools.ToolSetActivityFeel, keyFeedbackFeel, manualFeel)
	w.assertRating(t, detail, tools.ToolSetPerceivedEffort, keyFeedbackRPE,
		manualEffort*effortScale)
}

// assertRating compares one subjective rating with the value that was written,
// reporting the relative difference and never the reading itself.
func (w *writeEnv) assertRating(
	t *testing.T, detail map[string]any, tool, field string, want float64,
) {
	t.Helper()

	got, present := nested(detail, "feedback", field)
	if !present {
		t.Errorf("%s wrote a rating the activity record does not carry", tool)
		return
	}
	if got != want {
		t.Errorf("%s: the %s read back differs from the value written by %.3f%%",
			tool, field, 100*relative(got-want, want))
	}
}

// readActivity reads the created activity's own record.
func (w *writeEnv) readActivity(t *testing.T, id int64) map[string]any {
	t.Helper()

	detail := w.call(t, tools.ToolGetActivity, map[string]any{argActivityID: id})
	if got := identifier(t, detail, tools.ToolGetActivity, argActivityID); got != id {
		t.Fatalf("%s answered for a different activity than the one created",
			tools.ToolGetActivity)
	}
	return detail
}

// assertActivityIsGone proves the activity was removed.
func (w *writeEnv) assertActivityIsGone(t *testing.T, id int64) {
	t.Helper()

	result := w.rawCall(t, tools.ToolGetActivity, map[string]any{argActivityID: id})
	if !result.IsError {
		t.Errorf("%s still answers for an activity %s removed",
			tools.ToolGetActivity, tools.ToolDeleteActivity)
	}
}

// otherActivityType picks a reclassification target out of Garmin's own catalog.
//
// It is read rather than pinned, so no key of this server's is asserted to exist in
// Garmin's catalog, and the one key that must not be chosen is the one the activity
// already carries: reclassifying to the current type would prove nothing.
func (w *writeEnv) otherActivityType(t *testing.T, current string) string {
	t.Helper()

	catalog := w.call(t, tools.ToolGetActivityTypes, nil)
	rows, _ := catalog["activity_types"].([]any)

	for _, row := range rows {
		object, ok := row.(map[string]any)
		if !ok {
			continue
		}
		if hidden, _ := object["is_hidden"].(bool); hidden {
			continue
		}
		if key, ok := object[keyTypeKey].(string); ok && key != "" && key != current {
			return key
		}
	}
	t.Fatalf("%s named no activity type this suite could reclassify to",
		tools.ToolGetActivityTypes)
	return ""
}

// firstEventType takes an event-type key from the closed set the api package
// validates against, rather than spelling one out here a second time.
func firstEventType(t *testing.T) string {
	t.Helper()

	keys := api.EventTypeKeys()
	if len(keys) == 0 {
		t.Fatal("the api package declares no event-type keys, so no event type can be written")
	}
	return keys[0]
}

// merged returns the shared activity argument with one more field, without mutating
// the shared map.
func merged(base map[string]any, key string, value any) map[string]any {
	out := make(map[string]any, len(base)+1)
	for name, existing := range base {
		out[name] = existing
	}
	out[key] = value
	return out
}

//go:build garminlive

package live

import (
	"strconv"
	"sync/atomic"
	"time"
)

// A nameLabel is the middle field of a generated name. It is a defined type with a
// closed set of values rather than a free string, because the sweeper parses that field
// and decides on it: a label a caller invented would render a name the sweeper cannot
// recognise, and the leftover it names would survive every later run.
type nameLabel string

// The labels this suite renders. Each names what the object is for, and nothing about
// the account.
const (
	labelNameActivity        nameLabel = "activity"
	labelNameActivityRenamed nameLabel = "activity-renamed"
	labelNameBatch           nameLabel = "batch"
	labelNameCourse          nameLabel = "course"
	labelNameCustomFood      nameLabel = "customfood"
	labelNameDescription     nameLabel = "description"
	labelNameGear            nameLabel = "gear"
	labelNameStrength        nameLabel = "strength"
	labelNameStrengthWorkout nameLabel = "strengthworkout"
	labelNameTemplate        nameLabel = "template"
	labelNameWalkRun         nameLabel = "walkrun"
	labelNameWorkout         nameLabel = "workout"
	labelNameWorkoutUpdated  nameLabel = "workout-updated"
	labelNameZ2Walk          nameLabel = "z2walk"
)

// suiteLabels is the whole set, and it is the sweeper's allowlist. A label added above
// and not here renders names no sweep will ever recognise, so the two are one list.
func suiteLabels() []nameLabel {
	return []nameLabel{
		labelNameActivity, labelNameActivityRenamed, labelNameBatch, labelNameCourse,
		labelNameCustomFood, labelNameDescription, labelNameGear, labelNameStrength,
		labelNameStrengthWorkout, labelNameTemplate, labelNameWalkRun, labelNameWorkout,
		labelNameWorkoutUpdated, labelNameZ2Walk,
	}
}

// A nameSequence renders the generated names of one run.
//
// It holds the run's own stamp rather than reading a clock per name. One run is one
// instant by definition, and a clock read per name made every generated name depend on
// what the clock did between two calls: a step backwards across the start of the run
// would render a name the sweeper could later mistake for an earlier run's.
type nameSequence struct {
	startedAt time.Time
	counter   atomic.Int64
}

// newNameSequence starts a sequence at one instant, which the caller supplies rather
// than the sequence reading a clock of its own.
func newNameSequence(startedAt time.Time) *nameSequence {
	return &nameSequence{startedAt: startedAt.UTC()}
}

// name builds a recognisable, unique name for one created object.
//
// The shape is what the sweeper parses: the reserved prefix, a declared label, the run
// stamp that separates runs and orders them against this one, and a counter that starts
// at one and separates objects inside one run. It names no account and no person.
// isPreviousRunObject is the reader of this format, and the two must change together.
func (s *nameSequence) name(label nameLabel) string {
	return objectPrefix + string(label) + "-" +
		strconv.FormatInt(s.startedAt.Unix(), 10) + "-" +
		strconv.FormatInt(s.counter.Add(1), 10)
}

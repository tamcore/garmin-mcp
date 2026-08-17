//go:build garminlive

package live

import (
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// An ownedKind is the class of Garmin object an identifier names.
//
// The three classes are kept apart rather than folded into one identifier set,
// because Garmin's identifier spaces are independent: a workout id and an activity
// id may collide, and a guard that ignored the class would then treat one account
// object as proof of ownership of a different one.
type ownedKind int

const (
	kindActivity ownedKind = iota + 1
	kindWorkout
	kindSchedule
	kindCourse
)

// The class labels a refusal message and a guard probe use.
const (
	labelActivity = "activity"
	labelWorkout  = "workout"
	labelSchedule = "calendar entry"
	labelCourse   = "course"
)

// String is the class name a refusal message uses. It names the class and never
// the identifier, so no failure prints an account object.
func (k ownedKind) String() string {
	switch k {
	case kindActivity:
		return labelActivity
	case kindWorkout:
		return labelWorkout
	case kindSchedule:
		return labelSchedule
	case kindCourse:
		return labelCourse
	default:
		return "unrecognised object"
	}
}

// createdField is the response field that carries the identifier of a newly
// created object of this class, or "" when a create of this class reports no
// identifier the guard can read.
func (k ownedKind) createdField() string {
	switch k {
	case kindActivity:
		return "activityId"
	case kindWorkout:
		return "workoutId"
	case kindCourse:
		return "courseId"
	default:
		return ""
	}
}

// nameField is the field that carries this class's name, in the create request this
// suite sends and in the object Garmin serves for it. The two are the same field, which
// is what makes the read-back comparable with what was sent.
//
// kindCourse's create response nests no other object, so this is the one field
// courseguard_test.go's own read-back (a list search rather than a per-item GET, since
// Garmin's course service has none) compares against.
func (k ownedKind) nameField() string {
	switch k {
	case kindActivity:
		return "activityName"
	case kindWorkout:
		return "workoutName"
	case kindCourse:
		return "courseName"
	default:
		return ""
	}
}

// itemPath is the per-object path this class is read from, or "" for a class that has
// no single-object read.
func (k ownedKind) itemPath(id int64) string {
	prefix := ""
	switch k {
	case kindActivity:
		prefix = client.PathActivityPrefix
	case kindWorkout:
		prefix = client.PathWorkoutPrefix
	default:
		return ""
	}
	return prefix + "/" + strconv.FormatInt(id, 10)
}

// ownedObjects is the ledger of everything this suite created and has not yet
// removed. It is the write analogue of readOnlyCaller: the guard consults it, so
// "a write test only ever mutates an object it created itself" is a property of the
// wiring rather than a promise about the tests.
//
// It is also the leak ledger. An entry that survives the suite is an object left on
// a real account, which TestMain reports as a failure.
type ownedObjects struct {
	mu  sync.Mutex
	ids map[ownedKind]map[int64]struct{}
}

func newOwnedObjects() *ownedObjects {
	return &ownedObjects{ids: map[ownedKind]map[int64]struct{}{}}
}

// The ledger has no unconditional "own this identifier" entry point, and that is
// deliberate. Ownership can be reached only through the three functions below, each of
// which derives the identifier from something Garmin said rather than from something a
// caller passed:
//
//   - ownCreated takes the identifier out of the create response Garmin returned *and*
//     the name Garmin serves for that identifier when it is read back.
//   - ownSwept reads the name Garmin returned for an object and requires it to be one
//     this suite generated on an earlier run.
//   - ownScheduled takes the calendar entry Garmin returned for a workout this suite
//     already owns.
//
// Go has no file-level visibility, so this is not a boundary the compiler enforces:
// a future test in this package could still call one of the three. What it does
// enforce is that there is no way to *declare* ownership. Every path into the ledger
// verifies, so the worst a careless caller can do is verify something twice.

// record adds one verified identifier. It is the single mutation point and is called
// only by the three verifying functions in this file.
func (o *ownedObjects) record(kind ownedKind, id int64) {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.ids[kind] == nil {
		o.ids[kind] = map[int64]struct{}{}
	}
	o.ids[kind][id] = struct{}{}
}

// A createdObject is one create as the guard learned it from Garmin: the identifier the
// create response named, the name this suite put in the create request, and the
// identifier *and* name Garmin serves for that identifier when the object is read back.
//
// storedID is not a formality and is not the same fact as id. id is what the create
// response claimed; storedID is what the object itself reports when it is fetched. A
// read-back that answers with some other object — a cache, a redirect, a gateway that
// resolved the path differently — carries that object's identifier, and comparing only
// the name would adopt it whenever the two names happened to agree. Names collide here
// by construction: a generated name carries a one-second run stamp and a counter, so two
// runs that start in the same second render the same names.
type createdObject struct {
	id       int64
	sent     string
	stored   string
	storedID int64
}

// ownCreated records a created object, and reports whether it could.
//
// An identifier alone is not evidence, which is the whole reason this takes four
// fields. Garmin assigns the identifier, and a service that deduplicated a create,
// answered from a cache or drifted could name an object this suite never made — after
// which the guard would happily let a test mutate and delete it. The three tools that
// create and then immediately mutate their own creation make that a live risk rather
// than a theoretical one.
//
// What is required instead is a binding: the object read back at that identifier must
// report that same identifier *and* carry the name this suite generated for this create,
// both read from Garmin rather than assumed, and that name must carry the reserved
// prefix. The read is per class, so the object must also be of the class the create
// claimed. A create that reports no identifier — a calendar create does not — leaves the
// object unowned, and every later mutation of it is then refused.
//
// The identifier half of that binding is what makes the name half safe. A generated name
// carries a run stamp of one-second resolution and a per-run counter, so two runs that
// start in the same second render byte-identical names: a stale or drifted create
// identifier naming the *other* run's object would pass a name comparison. Requiring the
// fetched object to report the identifier being adopted closes that, because the object
// this suite is about to mutate has to agree that it is the one addressed.
func (o *ownedObjects) ownCreated(kind ownedKind, created createdObject) bool {
	if created.id <= 0 || created.sent == "" || !hasSuitePrefix(&created.sent) {
		return false
	}
	if created.storedID != created.id || created.stored != created.sent {
		return false
	}
	o.record(kind, created.id)
	return true
}

// ownSwept records a leftover from an earlier run, and reports whether it could.
//
// name is the name Garmin returned for the object. It must be one this suite generated,
// parsed field by field rather than merely prefix-matched, and its run stamp must be
// strictly older than this run: an object this run created cannot be a leftover, and an
// object whose name does not parse is not this suite's whatever it starts with.
func (o *ownedObjects) ownSwept(kind ownedKind, name *string, id int64, before time.Time) bool {
	if id <= 0 || !isPreviousRunObject(name, before) {
		return false
	}
	o.record(kind, id)
	return true
}

// ownScheduled records a calendar entry Garmin reported for a workout this suite
// created, and reports whether it could.
//
// Both halves of the association come from the same calendar entry Garmin returned:
// the caller does not choose the entry identifier, it reads it back. The workout the
// entry points at must already be owned, so an entry for any other template — which
// would be the maintainer's — is refused.
func (o *ownedObjects) ownScheduled(workoutID, scheduledID int64) bool {
	if scheduledID <= 0 || !o.owns(kindWorkout, workoutID) {
		return false
	}
	o.record(kindSchedule, scheduledID)
	return true
}

// owns reports whether this suite created the named object.
func (o *ownedObjects) owns(kind ownedKind, id int64) bool {
	o.mu.Lock()
	defer o.mu.Unlock()

	_, present := o.ids[kind][id]
	return present
}

// release forgets one identifier after Garmin confirmed its removal. It is what
// turns the ledger into a leak ledger: what is left is what was not deleted.
func (o *ownedObjects) release(kind ownedKind, id int64) {
	o.mu.Lock()
	defer o.mu.Unlock()

	delete(o.ids[kind], id)
}

// identifiers returns the identifiers still owned for one class, so the end-of-suite
// removal can walk them without holding the lock while it dispatches.
func (o *ownedObjects) identifiers(kind ownedKind) []int64 {
	o.mu.Lock()
	defer o.mu.Unlock()

	out := make([]int64, 0, len(o.ids[kind]))
	for id := range o.ids[kind] {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// outstanding reports how many objects of each class are still owned, ordered by
// class so the report is deterministic. No identifier is returned.
func (o *ownedObjects) outstanding() []string {
	o.mu.Lock()
	defer o.mu.Unlock()

	var lines []string
	for _, kind := range deletionOrder() {
		if count := len(o.ids[kind]); count > 0 {
			lines = append(lines, fmt.Sprintf("%d %s object(s)", count, kind))
		}
	}
	return lines
}

// leakedObjects reports what the suite created and failed to remove, or "" when
// nothing leaked or the write layer never ran.
//
// It prints the count per class and never an identifier, and it goes through the
// suite's structured logger to stderr rather than stdout so it cannot be confused with
// a test result.
func leakedObjects() string {
	w := builtWriteEnv()
	if w == nil || w.owned == nil {
		return ""
	}
	w.removeOutstanding()

	lines := slices.Concat(w.owned.outstanding(), w.foods.outstanding(), w.weighins.outstanding())
	if len(lines) == 0 {
		return ""
	}

	report := "the write suite could not remove " + strings.Join(lines, ", ") +
		"; they carry the " + objectPrefix + " prefix, and the next run's sweeper removes them"
	suiteLogger().Error("live: the write suite could not remove everything it created",
		slog.String("outstanding", strings.Join(lines, ", ")),
		slog.String("prefix", objectPrefix))
	return report
}

// deletionOrder is the order the sweeper and the leak report walk the classes:
// calendar entries before the workouts they point at, so no sweep leaves a dangling
// entry behind. A course points at nothing else this suite owns, so its position in
// the order carries no such constraint.
func deletionOrder() []ownedKind {
	return []ownedKind{kindSchedule, kindActivity, kindWorkout, kindCourse}
}

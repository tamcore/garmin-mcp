//go:build garminlive

package live

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// The agreement tolerances between the decoded device file and Garmin's own summary
// of the same activity. Each one is the width of a legitimate representation
// difference, not a licence for a wrong derivation.
const (
	// distanceTolerance is relative. Garmin rounds the summary distance and the
	// file carries centimetres, so the two differ in the last digits and nowhere
	// else.
	distanceTolerance = 0.005

	// elapsedTolerance is in seconds. Both sides state the same total elapsed
	// time; the allowance covers a one-second rounding on each side.
	elapsedTolerance = 2.0

	// ascentTolerance is in metres. Both sides report the device's own barometric
	// total, so they agree to the metre. This is the tolerance the shipped
	// record-resumming defect broke by roughly a factor of two.
	ascentTolerance = 2.0

	// caloriesTolerance is in kilocalories. The device computes the figure once
	// and writes it into both the file and the upload, so a whole-unit rounding
	// is the only expected difference.
	caloriesTolerance = 1.0

	// sessionCoverage is the least share of the record stream a single-session
	// file's session window must contain. A session that collapses to a point —
	// the second shipped defect — covers almost none of it.
	sessionCoverage = 0.9

	// minComparedFields is how many figures the cross-check must actually have
	// compared. Below it the activity carried too little to prove anything and the
	// test fails rather than passing vacuously.
	minComparedFields = 4

	// minRecordsForCoverage is the record count below which the coverage
	// invariant says nothing useful.
	minRecordsForCoverage = 60

	// comparedFigures is how many figures the cross-check offers up for comparison.
	comparedFigures = 6
)

// summaryFigures are the summaryDTO fields this cross-check compares against the
// decoded file. It is a local decode on purpose: the point is to read Garmin's
// document independently of the tool that also reads it.
type summaryFigures struct {
	Distance        client.Number `json:"distance"`
	ElapsedDuration client.Number `json:"elapsedDuration"`
	Calories        client.Number `json:"calories"`
	AverageHR       client.Number `json:"averageHR"`
	MaxHR           client.Number `json:"maxHR"`
	ElevationGain   client.Number `json:"elevationGain"`
}

// analysed is one recent activity whose device file this suite could decode.
// The selection and decoding of that activity live in fitselect_test.go.

// while a fixture built from either one would not notice.
func TestFITSessionAgreesWithTheActivitySummary(t *testing.T) {
	e := liveEnv(t)
	a := analysedActivity(t)

	figures, err := e.summaryFiguresOf(t.Context(), a.id)
	if err != nil {
		t.Fatalf("reading the activity summary: %v", err)
	}

	session := a.summary.Sessions[0]
	compared := 0
	for _, c := range []agreement{
		{field: "session distance", fit: session.Distance, rest: figures.Distance, rel: distanceTolerance},
		{field: "session elapsed time", value: session.Seconds, present: true,
			rest: figures.ElapsedDuration, abs: elapsedTolerance},
		{field: "session ascent", fit: session.Ascent, rest: figures.ElevationGain, abs: ascentTolerance},
		{field: "session calories", fit: session.Calories, rest: figures.Calories, abs: caloriesTolerance},
		{field: "session average heart rate", fit: session.AvgHeartRate, rest: figures.AverageHR},
		{field: "session maximum heart rate", fit: session.MaxHeartRate, rest: figures.MaxHR},
	} {
		if c.compare(t) {
			compared++
		}
	}

	if compared < minComparedFields {
		t.Fatalf("only %d of %d figures were carried by both sources, want at least %d: "+
			"this activity proves too little and the check would pass vacuously",
			compared, comparedFigures, minComparedFields)
	}
}

// summaryFiguresOf reads Garmin's own summary of one activity and decodes the
// figures the file is compared against.
func (e *env) summaryFiguresOf(ctx context.Context, id client.ID) (summaryFigures, error) {
	record, err := e.details.Summary(ctx, e.session, id)
	if err != nil {
		return summaryFigures{}, err
	}

	var figures summaryFigures
	if len(record.Summary) == 0 {
		return figures, errors.New("the activity record carried no summaryDTO")
	}
	if err := json.Unmarshal(record.Summary, &figures); err != nil {
		return summaryFigures{}, fmt.Errorf("decoding the summaryDTO: %w", err)
	}
	return figures, nil
}

// agreement is one field compared between the decoded file and Garmin's summary.
//
// abs and rel are the two tolerance forms. A zero for both means the figures must be
// equal, which is what a heart rate in whole beats per minute must be.
type agreement struct {
	field string

	fit     api.FITNumber
	present bool
	value   float64

	rest client.Number

	abs float64
	rel float64
}

// compare reports whether both sources carried the figure, and fails the test when
// they carried it and disagreed.
//
// It never prints a reading. A failure names the field and the relative delta, so a
// failing live run cannot put the account's health data into a terminal or a log.
func (a agreement) compare(t *testing.T) bool {
	t.Helper()

	fit, ok := a.fitValue()
	rest, present := a.rest.Float64()
	if !ok || !present {
		return false
	}

	delta := math.Abs(fit - rest)
	if delta <= a.abs || (a.rel > 0 && relative(delta, rest) <= a.rel) {
		return true
	}
	t.Errorf("%s disagrees with the activity summary by %.3f%% (tolerance %s)",
		a.field, 100*relative(delta, rest), a.tolerance())
	return true
}

// fitValue reports the decoded figure, from either the optional reading or the plain
// one the segment carries as a float.
func (a agreement) fitValue() (float64, bool) {
	if a.present {
		return a.value, true
	}
	return a.fit.Value, a.fit.OK
}

// tolerance renders the allowance that was breached. It states a constant of this
// package and no measurement.
func (a agreement) tolerance() string {
	switch {
	case a.rel > 0:
		return fmt.Sprintf("%.2f%% relative", 100*a.rel)
	case a.abs > 0:
		return fmt.Sprintf("%g absolute", a.abs)
	default:
		return "exact"
	}
}

// relative reports delta as a fraction of the reference. A zero reference makes any
// non-zero delta total disagreement.
func relative(delta, reference float64) float64 {
	if reference == 0 {
		if delta == 0 {
			return 0
		}
		return 1
	}
	return delta / math.Abs(reference)
}

// TestFITSessionCoversTheWholeRecordStream is the invariant the collapsed-session
// defect broke.
//
// A file with one session describes one continuous recording, so that session's
// window must contain essentially every record in the file. A summary message whose
// window is read wrongly collapses to a point and leaves every per-session figure
// derived from the stream reading zero, which no fixture reveals because a fixture's
// expected values are derived the same wrong way.
func TestFITSessionCoversTheWholeRecordStream(t *testing.T) {
	_ = liveEnv(t)
	a := analysedActivity(t)

	records := len(a.activity.Records)
	if records < minRecordsForCoverage {
		t.Skip("not run — the chosen activity carries too few records to prove coverage")
	}

	session := a.summary.Sessions[0]
	covered := float64(session.Samples) / float64(records)
	if covered < sessionCoverage {
		t.Errorf("the single session covers %.1f%% of the record stream, want at least %.0f%%: "+
			"the session window is collapsing", 100*covered, 100*sessionCoverage)
	}
	if session.Seconds <= 0 {
		t.Error("the single session reports no elapsed time, so its window is empty")
	}
}

//go:build garminlive

package live

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"sync"
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
type analysed struct {
	id       client.ID
	activity api.FITActivity
	summary  api.FITSummary
	fileSize int
}

// errNoAnalysableActivity reports that no recent activity carried a device file this
// suite could analyse. It is wrapped with a tally, so a skip states which stage
// rejected each candidate rather than leaving the maintainer to guess.
var errNoAnalysableActivity = errors.New(
	"live: no recent activity carried a decodable single-session device file")

// tally counts why each candidate was rejected. It holds counts only: no identifier,
// no date and no measurement.
type tally struct {
	// held is what the account's own activity counter reports. It is read only
	// when the listing came back empty, and it is what separates an account that
	// holds nothing from a listing this server can no longer read.
	held      int64
	heldKnown bool

	listed     int
	attempted  int
	downloaded int
	decoded    int
	sessions   int
}

// Error renders the tally as the reason no activity could be chosen.
func (t tally) Error() string {
	held := "unread"
	if t.heldKnown {
		held = strconv.FormatInt(t.held, 10)
	}
	return fmt.Sprintf(
		"%v (the account counts %s activities; listed %d, attempted %d, downloaded %d, "+
			"decoded %d, single-session %d)",
		errNoAnalysableActivity, held, t.listed, t.attempted, t.downloaded, t.decoded, t.sessions)
}

// Unwrap lets errors.Is reach the sentinel.
func (t tally) Unwrap() error { return errNoAnalysableActivity }

// chosen memoizes the activity every FIT check works on, so the suite downloads one
// device file rather than one per test.
var chosen = sync.OnceValues(pickAnalysableActivity)

// analysedActivity returns the shared activity, skipping when the account holds none
// this suite can analyse. An account with no recorded activity is a legitimate state,
// not a defect in this server.
func analysedActivity(t *testing.T) *analysed {
	t.Helper()

	a, err := chosen()
	if errors.Is(err, errNoAnalysableActivity) {
		t.Skipf("not run — %v", err)
	}
	if err != nil {
		t.Fatalf("live: choosing an activity to analyse: %v", err)
	}
	return a
}

// pickAnalysableActivity walks the most recent activities and returns the first one
// whose device file decodes into exactly one session.
//
// One session is required rather than preferred: a multi-session file's summary is
// the sum of its sessions, and summing them here would re-derive the very figures
// this check exists to avoid re-deriving.
func pickAnalysableActivity() (*analysed, error) {
	e, err := shared()
	if err != nil {
		return nil, err
	}
	if e.skip != "" {
		return nil, errNoAnalysableActivity
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*requestTimeout)
	defer cancel()

	listing, err := e.recentActivities(ctx)
	if err != nil {
		return nil, err
	}

	counts := tally{listed: len(listing)}
	if counts.listed == 0 {
		if held, err := e.activities.Count(ctx, e.session); err == nil {
			counts.held, counts.heldKnown = held, true
		}
	}
	var lastFailure error
	for _, activity := range listing {
		if activity.ActivityID == nil || counts.attempted >= maxFITCandidates {
			continue
		}
		id, err := client.NewID(*activity.ActivityID)
		if err != nil {
			continue
		}
		counts.attempted++

		raw, err := e.download(ctx, id)
		if err != nil {
			lastFailure = err
			continue
		}
		counts.downloaded++

		candidate, err := analyse(id, raw)
		if err != nil {
			lastFailure = err
			continue
		}
		counts.decoded++

		if len(candidate.activity.Sessions) != 1 {
			continue
		}
		counts.sessions++
		return candidate, nil
	}
	if lastFailure != nil {
		return nil, fmt.Errorf("%w: last failure: %w", counts, lastFailure)
	}
	return nil, counts
}

// recentActivities reads one bounded page of the newest activities.
func (e *env) recentActivities(ctx context.Context) ([]api.Activity, error) {
	page, err := client.NewPage(0, 10)
	if err != nil {
		return nil, fmt.Errorf("building the activity page: %w", err)
	}
	listing, err := e.activities.List(ctx, e.session, api.ListQuery{Page: page})
	if err != nil {
		return nil, fmt.Errorf("listing recent activities: %w", err)
	}
	return listing.Activities, nil
}

// download streams one activity's device file into memory. Nothing is written to
// disk: the bytes live for the length of the analysis and are discarded with it.
func (e *env) download(ctx context.Context, id client.ID) ([]byte, error) {
	var sink bytes.Buffer
	if _, err := e.files.Download(ctx, e.session, id, api.FormatOriginal, &sink); err != nil {
		return nil, fmt.Errorf("downloading the device file: %w", err)
	}
	return sink.Bytes(), nil
}

// analyse decodes and summarizes one downloaded device file.
func analyse(id client.ID, raw []byte) (*analysed, error) {
	activity, err := api.ParseFITActivity(raw, api.FITLimits{})
	if err != nil {
		return nil, fmt.Errorf("decoding the device file: %w", err)
	}
	return &analysed{
		id:       id,
		activity: activity,
		summary:  api.AnalyzeFIT(activity),
		fileSize: len(raw),
	}, nil
}

// TestFITSessionAgreesWithTheActivitySummary is the check a fixture structurally
// cannot make.
//
// Two sources describe the same outing: the device file this server decodes, and the
// summary Garmin computed when the device uploaded it. Neither is derived from the
// other inside this repository, so a wrong derivation here disagrees with Garmin,
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

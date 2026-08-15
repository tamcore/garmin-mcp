//go:build garminlive

package live

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// This file picks the one activity the FIT cross-checks run against and decodes it.
//
// The choice is made once for the whole suite and is deliberately narrow: a recent
// activity that carries a device file, decodes, and holds exactly one session. An
// account with no such activity is a legitimate state and a skip with a counted
// reason, never a silent pass.

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
		// An activity this suite created carries no device file, so downloading it
		// would burn a candidate slot and could push the whole FIT cross-check into
		// skipping. The write half is opt-in and short-lived, but the read half must
		// not depend on the order the two ran in.
		if hasSuitePrefix(activity.ActivityName) {
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

		candidate, err := analyse(ctx, id, raw)
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
func analyse(ctx context.Context, id client.ID, raw []byte) (*analysed, error) {
	activity, err := api.ParseFITActivity(ctx, raw, api.FITLimits{})
	if err != nil {
		return nil, fmt.Errorf("decoding the device file: %w", err)
	}
	summary, err := api.AnalyzeFIT(ctx, activity)
	if err != nil {
		return nil, fmt.Errorf("analysing the device file: %w", err)
	}
	return &analysed{
		id:       id,
		activity: activity,
		summary:  summary,
		fileSize: len(raw),
	}, nil
}

// TestFITSessionAgreesWithTheActivitySummary is the check a fixture structurally
// cannot make.
//
// Two sources describe the same outing: the device file this server decodes, and the
// summary Garmin computed when the device uploaded it. Neither is derived from the
// other inside this repository, so a wrong derivation here disagrees with Garmin,

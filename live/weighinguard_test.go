//go:build garminlive

package live

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// This file adds the one mutation shape neither writeguard_test.go's int64-keyed
// machinery nor foodguard_test.go's string-keyed one covers: delete_weigh_ins'
// argument surface names only a calendar date and a delete_all flag
// (internal/tools/weighindelete.go's deleteWeighInsInput) — it carries no
// identifier at all, unlike delete_workout or delete_activity, whose one argument
// is the object to remove. The tool's own contract therefore cannot express
// "delete only the weigh-in this suite created": with delete_all at its default of
// true, the argument surface alone would let a stray call remove every weigh-in
// Garmin holds for the day, the maintainer's own included.
//
// What the tool's arguments cannot say, the wire request still can. Garmin's own
// delete_weigh_ins implementation reads the day and fans out one DELETE per sample
// found (api.Weight.DeleteWeighIns), and each of those requests carries the one
// thing the tool omits: a specific sample identifier, in the path itself
// ("/weight-service/weight/{date}/byversion/{samplePk}"). weighInLedger and the
// guard below inspect that identifier one level below the tool's own argument
// surface, and refuse any sample this suite did not create before the request ever
// reaches Garmin — the same guarantee ownedObjects gives every other class of
// owned object, applied to the one tool whose own arguments cannot express it.
// weighinwrite_test.go additionally requires a separate acknowledgement before
// exercising delete_weigh_ins at all, because that structural gap in the tool's own
// contract is worth stating plainly rather than only defending against in code.

// weighInLedger is the ownership ledger for one weigh-in sample.
//
// Garmin's weigh-in write answers with the weight and the unit it recorded, never
// the sample identifier the delete path targets
// (internal/tools/weighinwrites.go's AddWeighInResult and
// AddWeighInWithTimestampsResult carry no such field). Ownership is therefore
// learned the same way foodLedger learns a food-log entry's identifier: the day is
// read immediately before and immediately after the write, and the one sample that
// appears only in the second reading is adopted. own is the only path into the
// ledger, for the same reason ownedObjects.record is private: there is no way to
// declare ownership, only to present the evidence.
type weighInLedger struct {
	mu sync.Mutex
	// ids maps every owned sample identifier to the calendar date it was recorded
	// against, which the per-day delete path needs.
	ids map[int64]string
}

func newWeighInLedger() *weighInLedger {
	return &weighInLedger{ids: map[int64]string{}}
}

// own adopts one sample identifier, given the day's sample identifiers as read from
// Garmin immediately before and immediately after the write. A write that produced
// no new sample, or more than one and so cannot be told apart, is refused rather
// than guessed at — the same rule foodLedger.ownLogEntry applies to a food-log
// entry.
func (l *weighInLedger) own(before, after map[int64]bool, date string) (int64, bool) {
	if date == "" {
		return 0, false
	}
	found := int64(0)
	for id := range after {
		if before[id] {
			continue
		}
		if found != 0 {
			return 0, false
		}
		found = id
	}
	if found == 0 {
		return 0, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ids[found] = date
	return found, true
}

// owns reports whether this suite created the named weigh-in sample.
func (l *weighInLedger) owns(id int64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, ok := l.ids[id]
	return ok
}

// release forgets one sample identifier after Garmin confirmed its removal.
func (l *weighInLedger) release(id int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.ids, id)
}

// entries returns every owned sample identifier with the calendar date it was
// recorded against.
func (l *weighInLedger) entries() map[int64]string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make(map[int64]string, len(l.ids))
	for id, date := range l.ids {
		out[id] = date
	}
	return out
}

// outstanding reports how many weigh-in samples are still owned, the same shape
// ownedObjects.outstanding and foodLedger.outstanding report for their own classes.
// No identifier or date is returned.
func (l *weighInLedger) outstanding() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.ids) == 0 {
		return nil
	}
	return []string{fmt.Sprintf("%d weigh-in object(s)", len(l.ids))}
}

// doWeighIn handles every weigh-in mutation, and reports whether it recognised the
// request at all. It is consulted before classifyMutation, the same way
// foodguard_test.go's doNutrition is, and it is the only place the int64-keyed
// weighInLedger is touched.
func (c writeCaller) doWeighIn(
	ctx context.Context, principal string, req *http.Request,
) (*http.Response, error, bool) {
	if req.URL == nil {
		return nil, nil, false
	}
	switch {
	case req.Method == http.MethodPost && req.URL.Path == client.PathWeightUserWeight:
		// A weigh-in create needs no ownership check on the way in — nothing is
		// targeted yet — and no adoption on the way out, because the response
		// carries no identifier this ledger could record (see weighInLedger's own
		// doc comment). Ownership of the recorded sample is established
		// afterward, by weighInLedger.own, once the test itself reads the day's
		// weigh-ins back and finds it.
		resp, err := c.inner.Do(ctx, principal, req)
		return resp, err, true
	case req.Method == http.MethodDelete &&
		strings.HasPrefix(req.URL.Path, client.PathWeightDeletePrefix+"/"):
		resp, err := c.weighInDelete(ctx, principal, req)
		return resp, err, true
	default:
		return nil, nil, false
	}
}

// weighInDelete admits one per-sample delete only when the sample identifier the
// path carries is one this suite created. Every entry delete_weigh_ins fans out to
// Garmin passes through here individually, so a day carrying any sample this suite
// did not create is refused before that sample's own DELETE ever leaves the
// process — whatever the tool's own delete_all argument asked for.
func (c writeCaller) weighInDelete(
	ctx context.Context, principal string, req *http.Request,
) (*http.Response, error) {
	id, ok := weighInSampleFromPath(req.URL.Path)
	if !ok || !c.weighins.owns(id) {
		return nil, fmt.Errorf(
			"live: refusing to delete a weigh-in this suite did not create")
	}
	return c.inner.Do(ctx, principal, req)
}

// weighInSampleFromPath reads the sample identifier out of a weigh-in delete path:
// client.PathWeightDeletePrefix + "/{date}/byversion/{samplePk}"
// (internal/garmin/api/weightdelete.go's weighInDeletePath).
func weighInSampleFromPath(path string) (int64, bool) {
	rest, found := strings.CutPrefix(path, client.PathWeightDeletePrefix+"/")
	if !found {
		return 0, false
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 3 || parts[1] != client.PathWeightByVersionSegment {
		return 0, false
	}
	id, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// keepCleanWeighIn is keepClean for a weigh-in sample: the identifier space
// weighInLedger tracks rather than ownedObjects' or foodLedger's, so it needs its
// own removal path, keyed by the calendar date the delete path requires. It lives
// here, next to the ledger, rather than in writecleanup_test.go, so that file stays
// inside the package's 400-line limit.
func (w *writeEnv) keepCleanWeighIn(t *testing.T, id int64, date string) {
	t.Helper()

	t.Cleanup(func() {
		if !w.weighins.owns(id) {
			return
		}
		if err := w.removeWeighIn(date); err != nil {
			t.Errorf("live: the weigh-in this test created could not be removed and is left "+
				"on the account: %s", safeError(err))
			return
		}
		w.weighins.release(id)
	})
}

// removeOutstandingWeighIns deletes every weigh-in sample still in the weigh-in
// ledger. A leftover here is otherwise unreachable on a later run: a weigh-in
// carries no name for isPreviousRunObject to recognise the way a workout or an
// activity does, so a killed run's sample can only be found by re-reading the day
// it was recorded against. writecleanup_test.go's removeOutstanding calls it
// alongside the food-ledger and ownedObjects equivalents.
func (w *writeEnv) removeOutstandingWeighIns() int {
	left := 0
	for id, date := range w.weighins.entries() {
		if err := w.removeWeighIn(date); err != nil {
			suiteLogger().Error(
				"live: a weigh-in this suite created could not be removed",
				slog.String("reason", safeError(err)))
			left++
			continue
		}
		w.weighins.release(id)
	}
	return left
}

// removeWeighIn deletes the one weigh-in sample recorded on date through the weight
// client, requiring the day to carry exactly one sample. That is safe here and only
// here: keepCleanWeighIn and removeOutstandingWeighIns are reached only for a date
// weighinwrite_test.go's own pre-flight check already proved carried none of the
// account's own weigh-ins before this suite added its own, so the one sample
// confirmAll=false demands is this suite's.
func (w *writeEnv) removeWeighIn(date string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*requestTimeout)
	defer cancel()

	parsed, err := client.ParseDate(date)
	if err != nil {
		return fmt.Errorf("the recorded weigh-in date is unusable: %w", err)
	}
	_, err = w.weight.DeleteWeighIns(ctx, w.session, parsed, false)
	return err
}

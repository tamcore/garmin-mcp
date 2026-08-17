package api

import (
	"context"
	"fmt"
	"net/http"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// maxWeighInDeletionsPerDay bounds how many per-day entries DeleteWeighIns
// will fan a delete out to. A real manual weigh-in day is at most a handful of
// entries; a day reporting more than this is refused outright rather than
// deleting a truncated subset of a response that no longer looks like a
// day of real weigh-ins.
const maxWeighInDeletionsPerDay = 50

// WeighInDeletion reports one weigh-in Garmin actually deleted.
type WeighInDeletion struct {
	SamplePK client.ID
	Result   WriteResult
}

// DeleteWeighInsResult is what DeleteWeighIns reports: every deletion actually
// dispatched, in the order the day listed them. Its length is the number of
// weigh-ins removed.
type DeleteWeighInsResult struct {
	Deleted []WeighInDeletion
}

// DeleteWeighIns removes every weigh-in recorded for date.
//
// It first reads the day through GetDailyWeighIns to discover which samples
// exist — the same lookup delete_weigh_ins performs upstream — and only then
// dispatches one DELETE per sample. Three refusals keep a fan-out delete from
// ever removing more than the caller asked for or less than it believes it
// did:
//
//   - a day with more than one weigh-in is refused unless confirmAll is true,
//     matching upstream's delete_all gate (garminconnect/__init__.py:1334-1340)
//     — but as a hard error rather than upstream's silent "return None", so an
//     ambiguous multi-entry day can never be mistaken for a completed or a
//     no-op delete;
//   - a day carrying more entries than maxWeighInDeletionsPerDay is refused
//     outright rather than truncated, because deleting only some of an
//     unexpectedly large day is itself a partial, silent deletion;
//   - an entry whose samplePk this package cannot parse as a positive
//     identifier aborts the whole call before any DELETE is sent, rather than
//     skipping that one entry and deleting the rest.
//
// A day with no weigh-ins at all is not an error: it reports an empty result,
// matching upstream's own "no weigh-ins found" case
// (garminconnect/__init__.py:1330-1333).
//
// Its effect is EffectDelete for every dispatched request, so the retry layer
// never repeats one: Garmin gives no guarantee that a rejected delete was not
// already applied.
//
// Source: delete_weigh_ins, which reads get_daily_weigh_ins and then calls
// delete_weigh_in — DELETE
// "/weight-service/weight/{cdate}/byversion/{weight_pk}" — once per entry
// (garminconnect/__init__.py:1311-1345).
func (w *Weight) DeleteWeighIns(
	ctx context.Context, session client.Session, date client.Date, confirmAll bool,
) (DeleteWeighInsResult, error) {
	labelReq := writeRequest(client.OpDeleteWeighIns, client.EndpointWeightDelete,
		http.MethodDelete, "", client.EffectDelete)
	if err := requireDate(labelReq, date); err != nil {
		return DeleteWeighInsResult{}, err
	}

	day, err := w.GetDailyWeighIns(ctx, session, date)
	if err != nil {
		return DeleteWeighInsResult{}, err
	}
	// The raw list is read directly rather than through Measurements(), which
	// silently bounds at the much larger maxWeighInMeasurements: a delete fan-out
	// must refuse an oversized day outright, never delete a silently truncated
	// subset of it.
	entries := day.DateWeightList
	if len(entries) > maxWeighInDeletionsPerDay {
		return DeleteWeighInsResult{}, invalid(labelReq, fmt.Errorf(
			"%w: the day carries more than %d weigh-ins, refusing to delete a truncated subset",
			client.ErrValidation, maxWeighInDeletionsPerDay))
	}
	if len(entries) == 0 {
		return DeleteWeighInsResult{}, nil
	}
	if len(entries) > 1 && !confirmAll {
		return DeleteWeighInsResult{}, invalid(labelReq, fmt.Errorf(
			"%w: %d weigh-ins found for this date; set confirmAll to delete all of them",
			client.ErrValidation, len(entries)))
	}

	ids := make([]client.ID, 0, len(entries))
	for _, entry := range entries {
		raw, ok := entry.SamplePK.Int64Exact()
		if !ok {
			return DeleteWeighInsResult{}, invalid(labelReq, fmt.Errorf(
				"%w: a weigh-in on this date has no usable sample identifier",
				client.ErrValidation))
		}
		id, err := client.NewID(raw)
		if err != nil {
			return DeleteWeighInsResult{}, invalid(labelReq, err)
		}
		ids = append(ids, id)
	}

	result := DeleteWeighInsResult{Deleted: make([]WeighInDeletion, 0, len(ids))}
	for _, id := range ids {
		write, err := w.deleteWeighIn(ctx, session, date, id)
		if err != nil {
			return result, err
		}
		result.Deleted = append(result.Deleted, WeighInDeletion{SamplePK: id, Result: write})
	}
	return result, nil
}

// weighInDeletePath builds the delete path: a calendar date, the literal
// PathWeightByVersionSegment and a validated sample identifier, each its own
// path segment.
func weighInDeletePath(date client.Date, id client.ID) string {
	return client.PathWeightDeletePrefix + "/" + date.String() + "/" +
		client.PathWeightByVersionSegment + "/" + id.String()
}

// deleteWeighIn removes one weigh-in sample. It carries the same OpDeleteWeighIns
// label DeleteWeighIns itself uses: no upstream tool exposes this single-item
// delete on its own, so it needs no Op distinct from the fan-out that dispatches it.
//
// Source: delete_weigh_in, DELETE
// "/weight-service/weight/{cdate}/byversion/{weight_pk}"
// (garminconnect/__init__.py:1311-1323).
func (w *Weight) deleteWeighIn(
	ctx context.Context, session client.Session, date client.Date, id client.ID,
) (WriteResult, error) {
	req := writeRequest(client.OpDeleteWeighIns, client.EndpointWeightDelete,
		http.MethodDelete, weighInDeletePath(date, id), client.EffectDelete)
	if date.IsZero() {
		return WriteResult{}, invalid(req, fmt.Errorf("%w: a calendar date is required",
			client.ErrValidation))
	}
	if id.IsZero() {
		return WriteResult{}, invalid(req, fmt.Errorf("%w: a positive sample identifier is required",
			client.ErrValidation))
	}

	payload, err := w.req.write(ctx, session, req, nil)
	if err != nil {
		return WriteResult{}, err
	}
	return newWriteResult(payload), nil
}

//go:build garminlive

package live

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// The nutrition surface adds two mutation shapes the int64-keyed machinery in
// writeguard_test.go and owned_test.go cannot express:
//
//   - a custom food's identifier (api.FoodID) is a 32-character hex UUID or a
//     FatSecret decimal string, never a Garmin int64, and its create response nests
//     the identifier and name inside a foodMetaData object rather than at the top
//     level;
//   - a food-log entry (api.LogID) is a hex UUID that Garmin's log PUT never echoes
//     back at all — the identifier is discoverable only by re-reading the day's food
//     log, the same way a calendar entry is discoverable only by re-reading the
//     calendar.
//
// foodLedger is those two objects' ownership ledger. Like ownedObjects, it exposes no
// unconditional "own this" entry point: ownFood requires the same create-then-read-back
// binding ownCreated requires, and ownLogEntry itself derives the entry's identifier
// from the account's own food log, read immediately before and immediately after the
// write — the same trust ownScheduled places in a calendar read — rather than trusting
// an identifier a caller already picked out.
type foodLedger struct {
	mu sync.Mutex
	// foods holds every custom-food identifier this run owns.
	foods map[string]struct{}
	// logs maps every owned food-log identifier to the meal date it was logged
	// against, which DeleteFoodLog needs and Garmin's own delete path carries in
	// the URL rather than the body.
	logs map[string]string
	// settingsDate is the one date the running test has declared it will write
	// through set_nutrition_daily_settings, so doNutrition can admit that
	// unownable endpoint for exactly that date and refuse every other one. See
	// doNutrition's admission comment.
	settingsDate string
}

func newFoodLedger() *foodLedger {
	return &foodLedger{foods: map[string]struct{}{}, logs: map[string]string{}}
}

// ownFood records a custom-food identifier once the object Garmin serves back for it
// reports both that identifier and the suite-prefixed name this create sent.
func (f *foodLedger) ownFood(sentName, createdID, storedID, storedName string) bool {
	if createdID == "" || storedID != createdID || storedName != sentName || !hasSuitePrefix(&sentName) {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.foods[createdID] = struct{}{}
	return true
}

// ownsFood reports whether this suite created the named custom food.
func (f *foodLedger) ownsFood(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.foods[id]
	return ok
}

// releaseFood forgets one custom-food identifier after Garmin confirmed its removal.
func (f *foodLedger) releaseFood(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.foods, id)
}

// foodIdentifiers returns every custom-food identifier still owned.
func (f *foodLedger) foodIdentifiers() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.foods))
	for id := range f.foods {
		out = append(out, id)
	}
	return out
}

// ownLogEntry adopts one food-log entry, given the day's food-log identifiers as
// read from Garmin immediately before and immediately after the write. It is the
// only path into f.logs, and it derives the identifier itself rather than trusting
// one a caller already picked out: ownership is granted only when after names
// exactly one identifier that before did not, the same way a calendar entry's
// identifier is only ever learned by reading the calendar back. A write that
// produced no new entry, or more than one and so cannot be told apart, is refused
// rather than guessed at.
func (f *foodLedger) ownLogEntry(before, after map[string]bool, mealDate string) (string, bool) {
	if mealDate == "" {
		return "", false
	}
	found := ""
	for id := range after {
		if before[id] {
			continue
		}
		if found != "" {
			return "", false
		}
		found = id
	}
	if found == "" {
		return "", false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logs[found] = mealDate
	return found, true
}

// ownsLog reports whether this suite logged the named food-log entry.
func (f *foodLedger) ownsLog(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.logs[id]
	return ok
}

// releaseLog forgets one food-log identifier after Garmin confirmed its removal.
func (f *foodLedger) releaseLog(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.logs, id)
}

// logEntries returns every owned food-log identifier with the meal date it was
// logged against.
func (f *foodLedger) logEntries() map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]string, len(f.logs))
	for id, date := range f.logs {
		out[id] = date
	}
	return out
}

// outstanding reports how many custom foods and food-log entries are still owned, the
// same shape ownedObjects.outstanding reports for its own three classes. No
// identifier is returned.
func (f *foodLedger) outstanding() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	var lines []string
	if count := len(f.foods); count > 0 {
		lines = append(lines, fmt.Sprintf("%d custom food object(s)", count))
	}
	if count := len(f.logs); count > 0 {
		lines = append(lines, fmt.Sprintf("%d food-log object(s)", count))
	}
	return lines
}

// doNutrition handles every nutrition mutation, and reports whether it recognised the
// request at all. It is consulted before classifyMutation, and it is the only place
// the string-keyed foodLedger is touched.
func (c writeCaller) doNutrition(
	ctx context.Context, principal string, req *http.Request,
) (*http.Response, error, bool) {
	if req.URL == nil {
		return nil, nil, false
	}
	switch {
	case req.Method == http.MethodPut && req.URL.Path == client.PathNutritionCustomFood:
		resp, err := c.customFoodPut(ctx, principal, req)
		return resp, err, true
	case req.Method == http.MethodDelete && isSingleSegmentUnder(req.URL.Path, client.PathNutritionCustomFood):
		resp, err := c.customFoodDelete(ctx, principal, req)
		return resp, err, true
	case req.Method == http.MethodPut &&
		(req.URL.Path == client.PathNutritionFoodLogs || req.URL.Path == client.PathNutritionFoodLogQuickAdd):
		// A food-log create needs no ownership check on the way in — nothing is
		// targeted yet — and no adoption on the way out, because the response
		// carries no identifier this ledger could record. Ownership of the logged
		// entry is established afterward, by ownLogEntry, once the test itself
		// reads the day's food log back and finds it.
		resp, err := c.inner.Do(ctx, principal, req)
		return resp, err, true
	case req.Method == http.MethodDelete && strings.HasPrefix(req.URL.Path, client.PathNutritionFoodLogPrefix+"/"):
		resp, err := c.foodLogDelete(ctx, principal, req)
		return resp, err, true
	case req.Method == http.MethodPut && isSingleSegmentUnder(req.URL.Path, client.PathNutritionSettingsPrefix):
		// set_nutrition_daily_settings mutates a real, permanent, account-wide
		// document nothing here can own, so the guard's only lever is the date:
		// settingsPut, in nutritionsettingsguard_test.go, admits this endpoint
		// only for the exact date the running test declared with
		// allowSettingsDate, and refuses every other date before the request
		// reaches Garmin.
		resp, err := c.settingsPut(ctx, principal, req)
		return resp, err, true
	default:
		return nil, nil, false
	}
}

// isSingleSegmentUnder reports whether path is prefix plus exactly one more
// non-empty segment, matching idAfter's own shape rule without requiring the
// segment to be numeric — a custom-food or nutrition-settings segment is not.
func isSingleSegmentUnder(path, prefix string) bool {
	rest, found := strings.CutPrefix(path, prefix+"/")
	return found && rest != "" && !strings.Contains(rest, "/")
}

// customFoodPut handles the one PUT path create_custom_food and update_custom_food
// share, distinguishing the two by whether the request body already names a food id.
func (c writeCaller) customFoodPut(
	ctx context.Context, principal string, req *http.Request,
) (*http.Response, error) {
	raw, read := takeBody(req, nameProbeLimit)
	if !read {
		return nil, fmt.Errorf("live: refusing a custom-food write whose body could not be read")
	}
	sentID := nestedStringField(raw, "", "foodId")
	sentName := nestedStringField(raw, "", "foodName")

	if sentID != "" {
		if !c.foods.ownsFood(sentID) {
			return nil, fmt.Errorf(
				"live: refusing to update a custom food this suite did not create")
		}
		return c.inner.Do(ctx, principal, req)
	}

	resp, err := c.inner.Do(ctx, principal, req)
	if err != nil {
		return resp, err
	}
	return c.adoptCustomFood(ctx, principal, sentName, req, resp)
}

// adoptCustomFood records a created custom food, once a search read-back for its own
// name confirms both the identifier and the name Garmin's create response claimed.
//
// The custom-food surface has no single-item GET the way an activity or a workout
// does, so the read-back here is a name search rather than a fetch by identifier —
// the same shape ownSwept already uses to recognise a leftover by name. A failure to
// verify leaves the object unowned rather than failing the run: it still carries this
// suite's reserved prefix, so a person can find it by hand even though this ledger
// cannot yet remove it.
func (c writeCaller) adoptCustomFood(
	ctx context.Context, principal, sentName string, model *http.Request, resp *http.Response,
) (*http.Response, error) {
	if resp == nil || resp.Body == nil || resp.StatusCode >= http.StatusMultipleChoices || sentName == "" {
		return resp, nil
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxSniffBytes+1))
	_ = resp.Body.Close()
	if err != nil || len(raw) > maxSniffBytes {
		return nil, fmt.Errorf("live: reading the custom-food create response so the created " +
			"food can be owned and removed")
	}
	resp.Body = io.NopCloser(bytes.NewReader(raw))

	encoding := resp.Header.Get(headerContentEncoding)
	createdID := nestedStringField(raw, encoding, "foodId")
	if createdID == "" {
		return resp, nil
	}

	storedID, storedName, err := c.storedCustomFood(ctx, principal, sentName, model)
	if err != nil {
		suiteLogger().Warn(
			"live: a created custom food could not be read back, so it is not owned and will "+
				"not be touched",
			slog.String("reason", safeError(err)))
		return resp, nil
	}
	c.foods.ownFood(sentName, createdID, storedID, storedName)
	return resp, nil
}

// storedCustomFood searches the account's own custom-food library for name and
// returns the identifier and name Garmin serves for the matching entry.
func (c writeCaller) storedCustomFood(
	ctx context.Context, principal, name string, model *http.Request,
) (id, storedName string, err error) {
	if model.URL == nil {
		return "", "", fmt.Errorf("no read-back request model is available")
	}

	target := *model.URL
	target.Path = client.PathNutritionCustomFood
	query := url.Values{}
	query.Set(client.QuerySearchExpression, name)
	query.Set(client.QueryStart, "0")
	query.Set(client.QueryLimit, "5")
	target.RawQuery = query.Encode()
	target.Fragment = ""

	probe, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return "", "", fmt.Errorf("building the custom-food read-back: %w", err)
	}
	probe.Header = model.Header.Clone()
	probe.Header.Del(headerContentType)

	resp, err := c.inner.Do(ctx, principal, probe)
	if err != nil {
		return "", "", fmt.Errorf("reading the created custom food back: %s", safeError(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= http.StatusMultipleChoices {
		return "", "", fmt.Errorf("reading the created custom food back answered with status %d",
			resp.StatusCode)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, nameProbeLimit+1))
	if err != nil || int64(len(raw)) > nameProbeLimit {
		return "", "", fmt.Errorf("the custom-food read-back could not be read")
	}

	document, err := decompressed(raw, resp.Header.Get(headerContentEncoding))
	if err != nil {
		return "", "", fmt.Errorf("the custom-food read-back is not readable")
	}
	var page struct {
		CustomFoods []json.RawMessage `json:"customFoods"`
	}
	if err := json.Unmarshal(document, &page); err != nil {
		return "", "", fmt.Errorf("the custom-food read-back is not a search page")
	}
	for _, item := range page.CustomFoods {
		itemName := nestedStringField(item, "", "foodName")
		itemID := nestedStringField(item, "", "foodId")
		if itemName == name && itemID != "" {
			return itemID, itemName, nil
		}
	}
	return "", "", fmt.Errorf("no matching custom food was found in the read-back")
}

// customFoodDelete admits a custom-food removal only for a food this suite created.
func (c writeCaller) customFoodDelete(
	ctx context.Context, principal string, req *http.Request,
) (*http.Response, error) {
	id, found := strings.CutPrefix(req.URL.Path, client.PathNutritionCustomFood+"/")
	if !found || id == "" || !c.foods.ownsFood(id) {
		return nil, fmt.Errorf(
			"live: refusing to delete a custom food this suite did not create")
	}
	return c.inner.Do(ctx, principal, req)
}

// foodLogDelete admits a food-log removal only when every log identifier the
// request's body names is one this suite logged and read back itself.
func (c writeCaller) foodLogDelete(
	ctx context.Context, principal string, req *http.Request,
) (*http.Response, error) {
	raw, read := takeBody(req, nameProbeLimit)
	if !read {
		return nil, fmt.Errorf("live: refusing a food-log delete whose body could not be read")
	}
	var body struct {
		LogIDs []string `json:"logIds"`
	}
	if err := json.Unmarshal(raw, &body); err != nil || len(body.LogIDs) == 0 {
		return nil, fmt.Errorf("live: refusing a food-log delete naming no log identifier")
	}
	for _, id := range body.LogIDs {
		if !c.foods.ownsLog(id) {
			return nil, fmt.Errorf(
				"live: refusing to delete a food-log entry this suite did not create")
		}
	}
	return c.inner.Do(ctx, principal, req)
}

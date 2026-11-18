package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// MaxDogsPerActivity bounds the dogs one activity records.
const MaxDogsPerActivity = 10

// SetDogs records which dogs joined one activity; an empty list records none.
//
// Source: the Garmin Connect web app's activity edit (connect.garmin.com
// 5.31.2.3), which PUTs the activity with metadataDTO set to the current
// metadata plus hasDog and dogProfileIds. The current metadata is read first so
// no other metadata field is dropped.
func (a *ActivityWrites) SetDogs(
	ctx context.Context, session client.Session, id client.ID, dogIDs []int64,
) (WriteResult, error) {
	req := writeRequest(client.OpSetActivityDogs, client.EndpointActivity,
		http.MethodPut, activityPath(id), client.EffectIdempotentWrite)
	if err := requireID(req, id); err != nil {
		return WriteResult{}, err
	}
	if len(dogIDs) > MaxDogsPerActivity {
		return WriteResult{}, invalid(req, fmt.Errorf("%w: too many dogs", client.ErrValidation))
	}
	for _, dog := range dogIDs {
		if dog <= 0 {
			return WriteResult{}, invalid(req, fmt.Errorf("%w: dog ids must be positive", client.ErrValidation))
		}
	}

	var current struct {
		Metadata map[string]json.RawMessage `json:"metadataDTO"`
	}
	read := readRequest(client.OpGetActivity, client.EndpointActivity, activityPath(id), nil)
	if _, err := a.req.read(ctx, session, read, &current); err != nil {
		return WriteResult{}, err
	}
	metadata := make(map[string]any, len(current.Metadata)+2)
	for key, value := range current.Metadata {
		metadata[key] = value
	}
	metadata["hasDog"] = len(dogIDs) > 0
	metadata["dogProfileIds"] = append([]int64{}, dogIDs...)

	body, err := jsonBody(req, struct {
		ActivityID string         `json:"activityId"`
		Metadata   map[string]any `json:"metadataDTO"`
	}{ActivityID: id.String(), Metadata: metadata})
	if err != nil {
		return WriteResult{}, err
	}
	req.Body = body
	return a.apply(ctx, session, req)
}

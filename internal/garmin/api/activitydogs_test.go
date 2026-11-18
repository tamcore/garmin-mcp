package api_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

func TestSetDogsMergesIntoTheCurrentMetadata(t *testing.T) {
	t.Parallel()

	current := `{"activityId":18446744,"metadataDTO":{"manualActivity":false,"hasDog":false,"lapCount":3}}`
	script := testkit.NewScript().With(activityWritePath(),
		testkit.JSON(http.StatusOK, current), testkit.JSON(http.StatusNoContent, ""))
	h := newHarness(t, script, client.Limits{})

	if _, err := newActivityWrites(t, h).SetDogs(t.Context(), h.session, mustID(t), []int64{4242}); err != nil {
		t.Fatalf("SetDogs() = %v", err)
	}
	requests := h.server.Requests()
	if len(requests) != 2 || requests[0].Method != http.MethodGet || requests[1].Method != http.MethodPut {
		t.Fatalf("requests = %d, want GET then PUT", len(requests))
	}
	body := decodeBody(t, requests[1].Body)
	metadata, ok := body["metadataDTO"].(map[string]any)
	if !ok {
		t.Fatalf("metadataDTO = %v", body["metadataDTO"])
	}
	if metadata["hasDog"] != true || metadata["lapCount"] != float64(3) {
		t.Errorf("metadataDTO = %v, want hasDog over the current metadata", metadata)
	}
	if ids, ok := metadata["dogProfileIds"].([]any); !ok || len(ids) != 1 || ids[0] != float64(4242) {
		t.Errorf("dogProfileIds = %v, want [4242]", metadata["dogProfileIds"])
	}
}

func TestSetDogsWithNoDogsClearsTheFlag(t *testing.T) {
	t.Parallel()

	current := `{"metadataDTO":{"hasDog":true,"dogProfileIds":[4242]}}`
	script := testkit.NewScript().With(activityWritePath(),
		testkit.JSON(http.StatusOK, current), testkit.JSON(http.StatusNoContent, ""))
	h := newHarness(t, script, client.Limits{})

	if _, err := newActivityWrites(t, h).SetDogs(t.Context(), h.session, mustID(t), nil); err != nil {
		t.Fatalf("SetDogs() = %v", err)
	}
	metadata := decodeBody(t, h.server.Requests()[1].Body)["metadataDTO"].(map[string]any)
	if ids, ok := metadata["dogProfileIds"].([]any); metadata["hasDog"] != false || !ok || len(ids) != 0 {
		t.Errorf("metadataDTO = %v, want hasDog false and an empty list", metadata)
	}
}

func TestSetDogsRefusesBadIDsBeforeAnyCall(t *testing.T) {
	t.Parallel()

	for name, ids := range map[string][]int64{
		"zero":     {0},
		"too many": make([]int64, api.MaxDogsPerActivity+1),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t, testkit.NewScript(), client.Limits{})
			_, err := newActivityWrites(t, h).SetDogs(t.Context(), h.session, mustID(t), ids)
			if !errors.Is(err, client.ErrValidation) {
				t.Errorf("SetDogs() = %v, want ErrValidation", err)
			}
			if len(h.server.Requests()) != 0 {
				t.Error("a refused write reached Garmin")
			}
		})
	}
}

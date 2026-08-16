package tools

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

func servingUnitsScript(body string) testkit.Script {
	return testkit.NewScript().With(client.PathNutritionCustomFoodServingUnits,
		testkit.JSON(http.StatusOK, body))
}

func TestGetCustomFoodServingUnitsDecodesTheCatalog(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, servingUnitsScript(`["G","ML","OZ"]`))
	out, err := h.svc.getCustomFoodServingUnits(h.ctx)
	if err != nil {
		t.Fatalf("getCustomFoodServingUnits() = %v", err)
	}
	if out.Count != 3 || len(out.Units) != 3 {
		t.Fatalf("units = %+v, want 3", out.Units)
	}
	if out.Units[0] != "G" {
		t.Errorf("first unit = %q, want G", out.Units[0])
	}
}

func TestGetCustomFoodServingUnitsRefusesAnAnonymousRequest(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, servingUnitsScript(`["G"]`))
	if _, err := h.svc.getCustomFoodServingUnits(t.Context()); !errors.Is(
		err, identity.ErrNoPrincipal) {
		t.Errorf("an anonymous request = %v, want ErrNoPrincipal", err)
	}
	if got := len(h.fake.Requests()); got != 0 {
		t.Errorf("the fake received %d requests, want 0", got)
	}
}

// TestGetCustomFoodServingUnitsReportsTruncationAtTheBound pins the flag: before it,
// the domain client's own 256-unit cut had no visible signal at all.
func TestGetCustomFoodServingUnitsReportsTruncationAtTheBound(t *testing.T) {
	t.Parallel()

	units := make([]string, 0, mirroredMaxServingUnits+5)
	for i := range mirroredMaxServingUnits + 5 {
		units = append(units, `"U`+strconv.Itoa(i)+`"`)
	}
	body := "[" + strings.Join(units, ",") + "]"
	h := newTrendHarness(t, servingUnitsScript(body))

	out, err := h.svc.getCustomFoodServingUnits(h.ctx)
	if err != nil {
		t.Fatalf("getCustomFoodServingUnits() = %v", err)
	}
	if out.Count != mirroredMaxServingUnits {
		t.Errorf("count = %d, want the bound %d", out.Count, mirroredMaxServingUnits)
	}
	if !out.Truncated {
		t.Error("a catalog at the bound does not report itself truncated")
	}
}

func TestGetCustomFoodServingUnitsReportsAnUnrecognizedShapeAsEmpty(t *testing.T) {
	t.Parallel()

	h := newTrendHarness(t, servingUnitsScript(`{"unexpected":true}`))
	out, err := h.svc.getCustomFoodServingUnits(h.ctx)
	if err != nil {
		t.Fatalf("getCustomFoodServingUnits() = %v", err)
	}
	if out.Count != 0 {
		t.Errorf("units = %+v, want none for an unrecognized shape", out.Units)
	}
}

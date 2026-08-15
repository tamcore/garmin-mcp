package tools

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

func cardioLifestylePath() string {
	return client.PathLifestyleLoggingPrefix + "/" + cardioDate
}

func TestReadLifestyleLogCarriesTheDocumentVerbatim(t *testing.T) {
	t.Parallel()

	body := `{"someLoggedBehaviour":[{"nested":true}]}`
	svc, fake := cardioService(t, cardioScript(cardioJSON(cardioLifestylePath(), body)))

	got, err := svc.readLifestyleLog(cardioContext(t), cardioDate)
	if err != nil {
		t.Fatalf("readLifestyleLog() = %v", err)
	}

	if !got.HasData {
		t.Error("HasData = false, want true")
	}
	if got.DocumentJSON != body {
		t.Errorf("DocumentJSON = %q, want the document verbatim", got.DocumentJSON)
	}
	if got.DocumentBytes != len(body) {
		t.Errorf("DocumentBytes = %d, want %d", got.DocumentBytes, len(body))
	}
	if calls := len(fake.Requests()); calls != 1 {
		t.Errorf("requests = %d, want 1: the day is in the path", calls)
	}
}

func TestReadLifestyleLogReportsADayWithNoLog(t *testing.T) {
	t.Parallel()

	svc, _ := cardioService(t, cardioScript(cardioRoute{
		path:     cardioLifestylePath(),
		behavior: testkit.Behavior{Status: http.StatusNoContent},
	}))

	got, err := svc.readLifestyleLog(cardioContext(t), cardioDate)
	if err != nil {
		t.Fatalf("readLifestyleLog() = %v", err)
	}
	if got.HasData || got.DocumentJSON != "" {
		t.Errorf("HasData/DocumentJSON = %t/%q, want false/empty", got.HasData, got.DocumentJSON)
	}
	if got.Date != cardioDate {
		t.Errorf("Date = %q, want the requested day", got.Date)
	}
}

func TestReadLifestyleLogRefusesADocumentOverTheBound(t *testing.T) {
	t.Parallel()

	body := `{"note":"` + strings.Repeat("x", DefaultMaxLifestyleLogBytes) + `"}`
	svc, _ := cardioService(t, cardioScript(cardioJSON(cardioLifestylePath(), body)))

	_, err := svc.readLifestyleLog(cardioContext(t), cardioDate)
	if !errors.Is(err, ErrResultTooLarge) {
		t.Fatalf("readLifestyleLog() over the bound = %v, want ErrResultTooLarge", err)
	}
	if strings.Contains(err.Error(), "xxxx") {
		t.Errorf("the refusal %q quotes the document, want authored advice only", err)
	}
}

func TestLifestyleLogLogValueReportsSizeNotContent(t *testing.T) {
	t.Parallel()

	got := LifestyleLog{
		HasData:       true,
		DocumentJSON:  `{"mood":"calm"}`,
		DocumentBytes: 15,
	}

	rendered := got.LogValue().String()
	if strings.Contains(rendered, "mood") || strings.Contains(rendered, "calm") {
		t.Errorf("the log value %q carries the document, want its size only", rendered)
	}
	if !strings.Contains(rendered, "bytes=15") {
		t.Errorf("the log value %q does not report the document size", rendered)
	}
}

func TestReadLifestyleLogReportsAGarminFailureWithoutThePayload(t *testing.T) {
	t.Parallel()

	svc, _ := cardioService(t, cardioScript(cardioFailure(cardioLifestylePath())))

	_, err := svc.readLifestyleLog(cardioContext(t), cardioDate)
	assertSanitizedGarminFailure(t, err)
}

// TestReadLifestyleLogDropsIdentifyingFields is the passthrough-egress regression for
// the one tool that returns a whole Garmin document. No field of it is sourced, so it
// is handed on under Garmin's own names — and the shared sanitiser is the only thing
// standing between a drifted document and an account identifier leaving the server.
func TestReadLifestyleLogDropsIdentifyingFields(t *testing.T) {
	t.Parallel()

	body := `{"userProfilePK":900001,"someLoggedBehaviour":[{"nested":true,` +
		`"startLatitude":1.5,"detail":{"ownerDisplayName":"fake-tester","score":7}}]}`
	svc, _ := cardioService(t, cardioScript(cardioJSON(cardioLifestylePath(), body)))

	got, err := svc.readLifestyleLog(cardioContext(t), cardioDate)
	if err != nil {
		t.Fatalf("readLifestyleLog() = %v", err)
	}

	for _, forbidden := range []string{
		keyUserProfilePK, fixtureProfilePK, keyStartLatitude, keyOwnerDisplay,
		cardioDisplayName,
	} {
		if strings.Contains(got.DocumentJSON, forbidden) {
			t.Errorf("the document carries %q, which identifies an account or a place", forbidden)
		}
	}
	if got.DroppedFields != 3 {
		t.Errorf("DroppedFields = %d, want 3", got.DroppedFields)
	}
	if !strings.Contains(got.DocumentJSON, "score") {
		t.Error("the sanitiser dropped a logged value, want only the identifiers removed")
	}
	if got.DocumentBytes != len(got.DocumentJSON) {
		t.Errorf("DocumentBytes = %d, want the size of what was returned", got.DocumentBytes)
	}
}

// TestReadLifestyleLogRefusesADocumentNestedPastTheBound proves the sanitiser's depth
// bound fails closed here: a document this server cannot walk whole is refused rather
// than returned with a silently missing subtree.
func TestReadLifestyleLogRefusesADocumentNestedPastTheBound(t *testing.T) {
	t.Parallel()

	depth := maxSanitizeDepth + 10
	body := strings.Repeat(`{"a":`, depth) + `1` + strings.Repeat(`}`, depth)
	svc, _ := cardioService(t, cardioScript(cardioJSON(cardioLifestylePath(), body)))

	_, err := svc.readLifestyleLog(cardioContext(t), cardioDate)
	if !errors.Is(err, ErrResultTooLarge) {
		t.Fatalf("readLifestyleLog() past the depth bound = %v, want ErrResultTooLarge", err)
	}
}

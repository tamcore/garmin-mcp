package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// Every fixture in this file is synthetic and generated here. Garmin's published
// document is never committed, and no test in this file reaches the network: the
// only server any of them talks to is a local one.

const (
	// syntheticCategories and syntheticPerCategory describe the generated
	// document. Both are above the compiled-in subset, which is what a fetched
	// catalog has to be to be believed.
	syntheticCategories  = 40
	syntheticPerCategory = 3

	// fetchedOnlyCategory is a category the compiled-in subset does not carry, so
	// a caller that validates against it proves the fetched catalog is in force.
	fetchedOnlyCategory = "BANDED_EXERCISES"
	fetchedOnlyExercise = "AB_TWIST"

	// builtinOnlyCategory is a category the published document does not carry.
	// The merge has to keep it, or a strength set that validated yesterday would
	// be refused today.
	builtinOnlyCategory = "UNKNOWN"

	// fillerMuscle is the muscle group every generated exercise reports.
	fillerMuscle = "CHEST"
)

// syntheticDocument renders a document with the shape Garmin publishes:
// {"categories":{"KEY":{"exercises":{"NAME":{"primaryMuscles":[...],...}}}}}.
//
// It is built from this package's own compiled-in rows plus synthetic filler, and
// not from Garmin's document, which is never committed. The compiled-in rows are
// there because a fetched catalog has to be recognizable as Garmin's taxonomy to
// be believed at all: a pile of invented categories is refused, which is the
// point of checkCatalogRecognized and is asserted separately below.
func syntheticDocument() []byte {
	return documentOf(syntheticRows())
}

// syntheticRows is the recognizable document as plain data, so a test can take it
// apart and put something implausible in its place.
func syntheticRows() map[string]map[string]catalogEntry {
	rows := map[string]map[string]catalogEntry{
		fetchedOnlyCategory: {
			fetchedOnlyExercise: {
				// The empty entry is deliberate: the published document carries
				// them, and they must be dropped rather than served.
				PrimaryMuscles:   []string{"ABS", "OBLIQUES", ""},
				SecondaryMuscles: []string{},
			},
		},
	}
	for _, row := range builtinCatalogRows() {
		if row.category == unknownCategory {
			continue
		}
		entries := map[string]catalogEntry{}
		for _, name := range row.names {
			entries[name] = catalogEntry{PrimaryMuscles: []string{fillerMuscle}}
		}
		// A leading digit is not expressible in the FIT enum, and a leading
		// underscore is how the published catalog spells it. It has to survive.
		entries[fmt.Sprintf("_3_WAY_%s", row.category)] = catalogEntry{
			PrimaryMuscles: []string{"CALVES"},
		}
		rows[row.category] = entries
	}
	for index := range syntheticCategories {
		entries := map[string]catalogEntry{}
		for position := range syntheticPerCategory {
			entries[fmt.Sprintf("SYNTHETIC_MOVEMENT_%d_%d", index, position)] = catalogEntry{
				PrimaryMuscles: []string{fillerMuscle},
			}
		}
		rows[fmt.Sprintf("SYNTHETIC_CATEGORY_%d", index)] = entries
	}
	return rows
}

// documentOf encodes catalog rows as the published document.
func documentOf(rows map[string]map[string]catalogEntry) []byte {
	type category struct {
		Exercises map[string]catalogEntry `json:"exercises"`
	}
	categories := make(map[string]category, len(rows))
	for key, entries := range rows {
		categories[key] = category{Exercises: entries}
	}

	raw, err := json.Marshal(map[string]any{categoriesMember: categories})
	if err != nil {
		panic(err)
	}
	return raw
}

// mustCategories returns the categories object of a synthetic document, so a test
// can wrap it in a document of its own.
func mustCategories(document []byte) []byte {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(document, &fields); err != nil {
		panic(err)
	}
	return fields[categoriesMember]
}

// serveDocument starts a local server that answers every request with body.
func serveDocument(t *testing.T, body []byte) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)
	return server
}

// TestFetchedCatalogReplacesTheCompiledInSubset is the behavior this whole path
// exists for: what the tool serves is the published catalog, not the subset.
func TestFetchedCatalogReplacesTheCompiledInSubset(t *testing.T) {
	t.Parallel()

	server := serveDocument(t, syntheticDocument())

	catalog, err := loadExerciseCatalog(t.Context(), server.Client(), server.URL)
	if err != nil {
		t.Fatalf("loadExerciseCatalog() = %v", err)
	}
	if catalog.Source() != CatalogSourceWeb {
		t.Fatalf("Source() = %q, want the fetched catalog", catalog.Source())
	}

	builtin := BuiltinExerciseCatalog()
	if len(catalog.Categories()) <= len(builtin.Categories()) {
		t.Errorf("the fetched catalog carries %d categories, the subset %d",
			len(catalog.Categories()), len(builtin.Categories()))
	}
	if catalog.ExerciseCount() <= builtin.ExerciseCount() {
		t.Errorf("the fetched catalog carries %d exercises, the subset %d",
			catalog.ExerciseCount(), builtin.ExerciseCount())
	}
	if err := catalog.Validate(fetchedOnlyCategory, fetchedOnlyExercise); err != nil {
		t.Errorf("a category only the fetched catalog knows = %v, want it accepted", err)
	}
	if err := builtin.Validate(fetchedOnlyCategory, ""); !errors.Is(err, client.ErrValidation) {
		t.Errorf("the subset already knew %q; the test proves nothing", fetchedOnlyCategory)
	}
}

// TestFetchedCatalogCarriesTheMuscleGroups covers the data the compiled-in subset
// never had, and the empty values the published document mixes into it.
func TestFetchedCatalogCarriesTheMuscleGroups(t *testing.T) {
	t.Parallel()

	server := serveDocument(t, syntheticDocument())

	catalog, err := loadExerciseCatalog(t.Context(), server.Client(), server.URL)
	if err != nil {
		t.Fatalf("loadExerciseCatalog() = %v", err)
	}
	category, known := catalog.Lookup(fetchedOnlyCategory)
	if !known {
		t.Fatalf("Lookup(%q) reported no such category", fetchedOnlyCategory)
	}
	if len(category.Exercises) != 1 {
		t.Fatalf("%d exercises, want the one the document carries", len(category.Exercises))
	}

	muscles := category.Exercises[0].PrimaryMuscles
	if want := []string{"ABS", "OBLIQUES"}; strings.Join(muscles, ",") != strings.Join(want, ",") {
		t.Errorf("PrimaryMuscles = %v, want %v with the empty entry dropped", muscles, want)
	}
	if len(category.Exercises[0].SecondaryMuscles) != 0 {
		t.Errorf("SecondaryMuscles = %v, want none", category.Exercises[0].SecondaryMuscles)
	}

	// The compiled-in subset has no muscle data at all, and must not invent any.
	for _, builtinCategory := range BuiltinExerciseCatalog().Types() {
		for _, exercise := range builtinCategory.Exercises {
			if len(exercise.PrimaryMuscles)+len(exercise.SecondaryMuscles) != 0 {
				t.Fatalf("the subset reports muscles for %q", exercise.Name)
			}
		}
	}
}

// TestFetchedCatalogKeepsEveryCompiledInCategory states the rule that keeps the
// fetch from being a regression: it may widen what validates, never narrow it.
func TestFetchedCatalogKeepsEveryCompiledInCategory(t *testing.T) {
	t.Parallel()

	server := serveDocument(t, syntheticDocument())

	catalog, err := loadExerciseCatalog(t.Context(), server.Client(), server.URL)
	if err != nil {
		t.Fatalf("loadExerciseCatalog() = %v", err)
	}
	for _, category := range BuiltinExerciseCatalog().Types() {
		if _, known := catalog.Lookup(category.Category); !known {
			t.Errorf("the fetched catalog dropped the compiled-in category %q", category.Category)
		}
		for _, exercise := range category.Exercises {
			if err := catalog.Validate(category.Category, exercise.Name); err != nil {
				t.Errorf("Validate(%q, %q) = %v after the fetch",
					category.Category, exercise.Name, err)
			}
		}
	}
	if _, known := catalog.Lookup(builtinOnlyCategory); !known {
		t.Errorf("%q is compiled in and absent from the document; the merge lost it",
			builtinOnlyCategory)
	}
}

// TestTheCatalogRequestCarriesExactlyTwoHeaders asserts what actually goes out,
// not merely the absence of a credential.
//
// The whole header set is pinned: a transport that added one of its own — the
// shared default transport adds Accept-Encoding, and any change here could add a
// fingerprint or a credential — fails this test rather than being described away.
// It runs against the dedicated client this read really uses, because the header
// set is a property of that client and its transport.
func TestTheCatalogRequestCarriesExactlyTwoHeaders(t *testing.T) {
	t.Parallel()

	var seen http.Header
	var method string
	var cookies int

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
		method = r.Method
		cookies = len(r.Cookies())
		_, _ = w.Write(syntheticDocument())
	}))
	t.Cleanup(server.Close)

	// The dedicated client, with the test certificate trusted and nothing else
	// changed, so the headers observed are the ones production sends.
	hc := newCatalogHTTPClient()
	transport, ok := hc.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("the catalog client uses %T, want its own *http.Transport", hc.Transport)
	}
	if transport == http.DefaultTransport {
		t.Fatal("the catalog client shares the process-wide default transport")
	}
	transport.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig

	if _, err := loadExerciseCatalog(t.Context(), hc, server.URL); err != nil {
		t.Fatalf("loadExerciseCatalog() = %v", err)
	}

	if method != http.MethodGet {
		t.Errorf("method = %q, want GET: this read never writes", method)
	}
	if cookies != 0 {
		t.Errorf("the request carried %d cookies, want none", cookies)
	}

	want := map[string]string{
		"Accept":     catalogAcceptHeader,
		"User-Agent": catalogUserAgent,
	}
	for name, values := range seen {
		expected, permitted := want[name]
		if !permitted {
			t.Errorf("the request carried an unexpected header %s: %v", name, values)
			continue
		}
		if len(values) != 1 || values[0] != expected {
			t.Errorf("header %s = %v, want [%q]", name, values, expected)
		}
	}
	for name := range want {
		if seen.Get(name) == "" {
			t.Errorf("header %s was not sent", name)
		}
	}
}

// TestCatalogFetchDoesNotFollowARedirect keeps the exception to one URL: a
// redirect is refused rather than followed to another host.
func TestCatalogFetchDoesNotFollowARedirect(t *testing.T) {
	t.Parallel()

	elsewhere := serveDocument(t, syntheticDocument())
	redirects := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirects++
		http.Redirect(w, r, elsewhere.URL, http.StatusFound)
	}))
	t.Cleanup(server.Close)

	catalog, err := loadExerciseCatalog(t.Context(), newCatalogHTTPClient(), server.URL)
	if err == nil {
		t.Fatal("a redirect was followed; the read must stay on the one permitted URL")
	}
	if catalog.Source() != CatalogSourceBuiltin {
		t.Errorf("Source() = %q, want the compiled-in fallback", catalog.Source())
	}
	if redirects != 1 {
		t.Errorf("the redirecting server was called %d times", redirects)
	}
}

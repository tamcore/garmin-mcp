package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// The failure half of the published-catalog read: every way it can go wrong, and
// what a caller is left with each time. The fixtures and the local servers come
// from exercisecatalogweb_internal_test.go, and nothing here reaches the network.

// TestEveryCatalogFailureFallsBackToTheCompiledInSubset is the start-up
// guarantee: whatever Garmin's CDN does, a usable catalog comes back and nothing
// here can stop a server from starting.
func TestEveryCatalogFailureFallsBackToTheCompiledInSubset(t *testing.T) {
	t.Parallel()

	// A document the byte cap is the only reason to refuse: it is well-formed and
	// plausible, and only its padding puts it over the bound.
	padded, err := json.Marshal(map[string]any{
		"categories": json.RawMessage(
			mustCategories(syntheticDocument())),
		"padding": strings.Repeat("a", MaxExerciseCatalogBytes),
	})
	if err != nil {
		t.Fatalf("encode the padded document: %v", err)
	}

	cases := map[string]http.HandlerFunc{
		"not found": func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "gone", http.StatusNotFound)
		},
		"server error": func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		},
		"malformed json": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("{not json"))
		},
		"truncated body": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"categories":{"SQUAT":{"exer`))
		},
		"no categories": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"categories":{}}`))
		},
		"categories without exercises": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"categories":{"SQUAT":{"exercises":{}},` +
				`"ROW":{"exercises":{}}}}`))
		},
		"smaller than the compiled-in subset": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"categories":{"SQUAT":{"exercises":` +
				`{"BACK_SQUAT":{"primaryMuscles":["GLUTES"]}}}}}`))
		},
		"oversized body": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(padded)
		},
	}

	for name, handler := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(handler)
			t.Cleanup(server.Close)

			assertFallback(t, server.Client(), server.URL)
		})
	}
}

// TestCatalogTransportFailuresFallBack covers the failures that never reach a
// handler: a refused connection, an untrusted certificate, and a deadline.
func TestCatalogTransportFailuresFallBack(t *testing.T) {
	t.Parallel()

	t.Run("connection refused", func(t *testing.T) {
		t.Parallel()

		closed := httptest.NewServer(http.HandlerFunc(
			func(http.ResponseWriter, *http.Request) {}))
		url := closed.URL
		closed.Close()

		assertFallback(t, newCatalogHTTPClient(), url)
	})

	t.Run("untrusted certificate", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewTLSServer(http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write(syntheticDocument())
			}))
		t.Cleanup(server.Close)

		// The dedicated client trusts the system roots only, which is what the
		// real read does, so the test certificate fails verification.
		assertFallback(t, newCatalogHTTPClient(), server.URL)
	})

	t.Run("timeout", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) {
				time.Sleep(2 * time.Second)
				_, _ = w.Write(syntheticDocument())
			}))
		t.Cleanup(server.Close)

		slow := server.Client()
		slow.Timeout = 20 * time.Millisecond
		assertFallback(t, slow, server.URL)
	})
}

// assertFallback runs one failing read and states what a caller is left with.
func assertFallback(t *testing.T, hc *http.Client, url string) {
	t.Helper()

	catalog, err := loadExerciseCatalog(t.Context(), hc, url)
	if err == nil {
		t.Fatal("the read reported success; the failure was not detected")
	}
	if catalog == nil {
		t.Fatal("no catalog came back; a start-up would have nothing to serve")
	}
	if catalog.Source() != CatalogSourceBuiltin {
		t.Errorf("Source() = %q, want the compiled-in fallback", catalog.Source())
	}
	if err := catalog.Validate("SQUAT", "BACK_SQUAT"); err != nil {
		t.Errorf("the fallback catalog is unusable: %v", err)
	}
}

// TestParseExerciseCatalogDropsWhatItCannotUse covers drift tolerance: a key the
// enum charset does not allow costs that entry, not the catalog.
func TestParseExerciseCatalogDropsWhatItCannotUse(t *testing.T) {
	t.Parallel()

	var document map[string]any
	if err := json.Unmarshal(syntheticDocument(), &document); err != nil {
		t.Fatalf("decode the synthetic document: %v", err)
	}
	categories, _ := document[categoriesMember].(map[string]any)
	categories["not a key"] = map[string]any{"exercises": map[string]any{"X": map[string]any{}}}
	categories["SQUAT"] = map[string]any{"exercises": map[string]any{
		"back squat; drop table": map[string]any{},
		"BACK_SQUAT":             map[string]any{},
	}}

	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encode the synthetic document: %v", err)
	}
	catalog, err := ParseExerciseCatalog(raw)
	if err != nil {
		t.Fatalf("ParseExerciseCatalog() = %v", err)
	}

	if _, known := catalog.Lookup("not a key"); known {
		t.Error("a malformed category key reached the catalog")
	}
	squat, known := catalog.Lookup("SQUAT")
	if !known {
		t.Fatal("the well-formed category was dropped with the malformed name")
	}
	for _, exercise := range squat.Exercises {
		if !isExerciseKey(exercise.Name) {
			t.Errorf("a malformed exercise name reached the catalog: %q", exercise.Name)
		}
	}
}

// TestCatalogSnapshotIsSafeForConcurrentReaders states why the process-wide cache
// needs no lock: the snapshot is immutable and every accessor copies.
func TestCatalogSnapshotIsSafeForConcurrentReaders(t *testing.T) {
	t.Parallel()

	server := serveDocument(t, syntheticDocument())
	catalog, err := loadExerciseCatalog(t.Context(), server.Client(), server.URL)
	if err != nil {
		t.Fatalf("loadExerciseCatalog() = %v", err)
	}
	want := catalog.ExerciseCount()

	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			for range 4 {
				types := catalog.Types()
				// A caller that mutates what it was handed must not be able to
				// reach what the next caller reads.
				types[0].Exercises = nil
				types[0].Category = "MUTATED"
				if got := catalog.ExerciseCount(); got != want {
					t.Errorf("ExerciseCount() = %d, want %d", got, want)
				}
				if err := catalog.Validate(fetchedOnlyCategory, ""); err != nil {
					t.Errorf("Validate() = %v", err)
				}
			}
		})
	}
	group.Wait()

	if catalog.Types()[0].Category == "MUTATED" {
		t.Error("a caller mutated the shared snapshot")
	}
}

// TestLoadExerciseCatalogUsesTheCompiledInURL states the boundary: the address of
// this read is in the binary, and nothing outside it contributes a host or a path.
func TestLoadExerciseCatalogUsesTheCompiledInURL(t *testing.T) {
	t.Parallel()

	const want = "https://connect.garmin.com/web-data/exercises/Exercises.json"
	if ExerciseCatalogURL != want {
		t.Errorf("ExerciseCatalogURL = %q, want %q", ExerciseCatalogURL, want)
	}

	// A cancelled context reaches the network with nothing, which is the cheapest
	// way to prove the exported entry point still answers with a usable catalog
	// and never touches Garmin from a test.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	catalog := LoadExerciseCatalog(ctx)
	if catalog == nil || catalog.Source() != CatalogSourceBuiltin {
		t.Fatalf("LoadExerciseCatalog() with a cancelled context = %v", catalog)
	}
}

// TestANilCatalogIsTheCompiledInSubset pins the contract every caller that was not
// handed a catalog relies on: the strength write path takes a *ExerciseCatalog, and
// a nil one must validate against the compiled-in subset rather than panic or
// accept everything.
func TestANilCatalogIsTheCompiledInSubset(t *testing.T) {
	t.Parallel()

	var absent *ExerciseCatalog

	if absent.Source() != CatalogSourceBuiltin {
		t.Errorf("Source() = %q on a nil catalog, want the compiled-in subset", absent.Source())
	}
	if absent.ExerciseCount() != BuiltinExerciseCatalog().ExerciseCount() {
		t.Errorf("ExerciseCount() = %d on a nil catalog, want the compiled-in count",
			absent.ExerciseCount())
	}
	if len(absent.Types()) != len(BuiltinExerciseCatalog().Types()) {
		t.Errorf("Types() carries %d categories on a nil catalog", len(absent.Types()))
	}
	if len(absent.Categories()) == 0 {
		t.Error("Categories() is empty on a nil catalog")
	}
	if err := absent.Validate("SQUAT", "BACK_SQUAT"); err != nil {
		t.Errorf("Validate() = %v on a nil catalog, want the compiled-in subset to answer", err)
	}
	if err := absent.Validate(fetchedOnlyCategory, ""); !errors.Is(err, client.ErrValidation) {
		t.Errorf("Validate(%q) = %v on a nil catalog, want it refused",
			fetchedOnlyCategory, err)
	}
}

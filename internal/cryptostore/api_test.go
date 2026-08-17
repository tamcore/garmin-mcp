package cryptostore

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"
	"testing"
)

// TestExportedFunctionSurfaceIsNarrow pins the exported function list. The
// package is the only place that touches key material, so an accidentally
// exported helper is a security regression, not a style nit.
func TestExportedFunctionSurfaceIsNarrow(t *testing.T) {
	want := []string{"Decrypt", "Encrypt", "GenerateKey", "LoadKey", "LoadOrCreateKey", "SealedKeyVersion"}

	// Every non-test .go file in this directory is parsed, including the
	// build-tagged platform files, so a symbol exported only on one GOOS is caught
	// too. parser.ParseFile is used directly because ParseDir ignores build tags
	// and is deprecated.
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}

	fset := token.NewFileSet()
	var got []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		got = append(got, exportedFuncNames(file)...)
	}
	sort.Strings(got)

	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("exported functions = %v, want %v", got, want)
	}
}

// exportedFuncNames lists exported top-level functions, excluding methods:
// accessors and redaction methods on the exported types are intentional.
func exportedFuncNames(file *ast.File) []string {
	var names []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || !fn.Name.IsExported() {
			continue
		}
		names = append(names, fn.Name.Name)
	}
	return names
}

// TestKeyringBackendIsOptional records that the file backend is always
// available: on every platform the keyring is a documented no-op today, so a
// CGO_ENABLED=0 build keeps working.
func TestKeyringBackendIsOptional(t *testing.T) {
	if keyringAvailable() {
		t.Fatal("no keyring backend is implemented yet, so keyringAvailable must report false")
	}
	if _, err := keyringLoad("garmin-mcp", 1); !errors.Is(err, errKeyringUnsupported) {
		t.Fatalf("keyringLoad error = %v, want errKeyringUnsupported", err)
	}
	if keyringPlatform() == "" {
		t.Fatal("keyringPlatform must name the platform it would use")
	}
}

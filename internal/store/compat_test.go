package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func legacyDocument() string {
	return `{"di_token":"` + testToken + `","di_refresh_token":"` + testRefreshToken +
		`","di_client_id":"` + testClientID + `"}`
}

func TestIsLegacyTokenDocumentDetectsByStructure(t *testing.T) {
	// The 0.3.10 note is explicit: structural detection beats a length threshold,
	// which misclassifies long paths as JSON and short JSON as a path.
	cases := map[string]struct {
		content string
		want    bool
	}{
		"full document":              {legacyDocument(), true},
		"padded with whitespace":     {"\n  " + legacyDocument() + "\n", true},
		"unknown extra fields":       {`{"di_token":"a","di_refresh_token":"b","extra":{"x":1}}`, true},
		"short but complete":         {`{"di_token":"a","di_refresh_token":"b"}`, true},
		"missing refresh token":      {`{"di_token":"a"}`, false},
		"empty token":                {`{"di_token":"","di_refresh_token":"b"}`, false},
		"token of the wrong type":    {`{"di_token":42,"di_refresh_token":"b"}`, false},
		"json array":                 {`["di_token","di_refresh_token"]`, false},
		"our own encrypted record":   {`{"schema":1,"version":1,"payload":"AQ=="}`, false},
		"not json":                   {"/home/user/.garminconnect", false},
		"empty":                      {"", false},
		"very long path-like string": {strings.Repeat("/very/long/path/segment", 40), false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := IsLegacyTokenDocument([]byte(tc.content)); got != tc.want {
				t.Fatalf("IsLegacyTokenDocument = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestImportLegacyTokenFileReadsA03xDirectory(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, legacyTokenFileName)
	if err := os.WriteFile(path, []byte(legacyDocument()), 0o600); err != nil {
		t.Fatalf("write legacy file: %v", err)
	}

	// The directory form is what upstream documents as GARMINTOKENS.
	set, err := ImportLegacyTokenFile(dir)
	if err != nil {
		t.Fatalf("ImportLegacyTokenFile: %v", err)
	}
	if set.Token() != testToken || set.RefreshToken() != testRefreshToken {
		t.Fatal("import lost the credentials")
	}
	if set.ClientID() != testClientID {
		t.Fatalf("import client id = %q, want %q", set.ClientID(), testClientID)
	}
	// The expiry comes from the unverified exp claim, which the synthetic JWT sets
	// to 2026-01-01T00:00:00Z. It is scheduling metadata only.
	if !set.ExpiresAt().Equal(testExpiry()) {
		t.Fatalf("import expiry = %v, want %v from the unverified exp claim", set.ExpiresAt(), testExpiry())
	}
}

func TestImportLegacyTokenFileRefusesAForeignDocument(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, legacyTokenFileName)
	if err := os.WriteFile(path, []byte(`{"schema":1,"version":1,"payload":"AQ=="}`), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	if _, err := ImportLegacyTokenFile(dir); !errors.Is(err, ErrIncompatibleTokenFile) {
		t.Fatalf("ImportLegacyTokenFile err = %v, want ErrIncompatibleTokenFile", err)
	}
}

func TestImportLegacyTokenFileReportsAbsenceAsErrNoTokens(t *testing.T) {
	if _, err := ImportLegacyTokenFile(tempDir(t)); !errors.Is(err, ErrNoTokens) {
		t.Fatalf("ImportLegacyTokenFile of an empty directory: err = %v, want ErrNoTokens", err)
	}
}

func TestImportLegacyTokenFileErrorNeverQuotesTheDocument(t *testing.T) {
	dir := tempDir(t)
	path := filepath.Join(dir, legacyTokenFileName)
	broken := `{"di_token":"` + testToken + `","di_refresh_token":`
	if err := os.WriteFile(path, []byte(broken), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	_, err := ImportLegacyTokenFile(dir)
	if err == nil {
		t.Fatal("a truncated document must be refused")
	}
	// Source: the 0.3.10 login() failure path logs only the source kind and the
	// length, never the tokenstore value, because it may be the refresh token.
	if strings.Contains(err.Error(), testToken) {
		t.Fatalf("the error leaked token material: %v", err)
	}
	if !strings.Contains(err.Error(), "bytes") {
		t.Fatalf("the error should report the document length instead: %v", err)
	}
}

func TestExportLegacyTokenFileWritesThe03xFormat(t *testing.T) {
	dir := tempDir(t)

	path, err := ExportLegacyTokenFile(dir, newTestTokens())
	if err != nil {
		t.Fatalf("ExportLegacyTokenFile: %v", err)
	}
	if want := filepath.Join(dir, legacyTokenFileName); path != want {
		t.Fatalf("export path = %q, want %q", path, want)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	if !IsLegacyTokenDocument(raw) {
		t.Fatalf("the export is not a 0.3.x document: %s", raw)
	}
	for _, field := range []string{`"di_token"`, `"di_refresh_token"`, `"di_client_id"`} {
		if !strings.Contains(string(raw), field) {
			t.Fatalf("the export is missing %s: %s", field, raw)
		}
	}

	reimported, err := ImportLegacyTokenFile(path)
	if err != nil {
		t.Fatalf("re-import: %v", err)
	}
	if reimported.Token() != testToken || reimported.RefreshToken() != testRefreshToken {
		t.Fatal("the export does not round-trip")
	}
}

func TestLooksLikeInlineTokenJSONUsesStructure(t *testing.T) {
	cases := map[string]bool{
		legacyDocument():                    true,
		"  " + legacyDocument():             true,
		`[{"di_token":"a"}]`:                true,
		"/home/user/.garminconnect":         false,
		strings.Repeat("/long/path", 60):    false,
		"":                                  false,
		`C:\Users\user\AppData\tokens.json`: false,
	}
	for value, want := range cases {
		if got := LooksLikeInlineTokenJSON(value); got != want {
			t.Fatalf("LooksLikeInlineTokenJSON(%.20q) = %v, want %v", value, got, want)
		}
	}
}

func TestParseInlineTokenJSONIsRefusedUnlessExplicitlyAllowed(t *testing.T) {
	_, err := ParseInlineTokenJSON(legacyDocument(), false)
	if !errors.Is(err, ErrInlineTokensRefused) {
		t.Fatalf("ParseInlineTokenJSON without the override: err = %v, want ErrInlineTokensRefused", err)
	}
	if strings.Contains(err.Error(), testRefreshToken) || strings.Contains(err.Error(), testToken) {
		t.Fatalf("the refusal leaked the inline token material: %v", err)
	}
}

func TestParseInlineTokenJSONAcceptsTheDocumentWhenAllowed(t *testing.T) {
	set, err := ParseInlineTokenJSON(legacyDocument(), true)
	if err != nil {
		t.Fatalf("ParseInlineTokenJSON: %v", err)
	}
	if set.Token() != testToken || set.RefreshToken() != testRefreshToken {
		t.Fatal("inline parsing lost the credentials")
	}
}

func TestParseInlineTokenJSONErrorNeverQuotesTheValue(t *testing.T) {
	broken := `{"di_token":"` + testToken + `","di_refresh_token":`

	_, err := ParseInlineTokenJSON(broken, true)
	if err == nil {
		t.Fatal("a truncated inline document must be refused")
	}
	if strings.Contains(err.Error(), testToken) {
		t.Fatalf("the error leaked token material: %v", err)
	}
	if !strings.Contains(err.Error(), "inline-json") {
		t.Fatalf("the error should name only the source kind: %v", err)
	}
}

package store

import (
	"encoding/json"
	"fmt"
	"path/filepath"
)

// IsLegacyTokenDocument reports whether raw is a 0.3.x garmin_tokens.json document.
//
// The test is structural: a JSON object with a non-empty string di_token and a
// di_refresh_token field. It is deliberately not a length threshold, which
// misclassifies a long legitimate path as JSON and a short JSON document as a path.
// Unknown extra fields are accepted, because upstream reads the document field by
// field.
//
// Source: _looks_like_json and Client.loads in python-garminconnect 0.3.10.
func IsLegacyTokenDocument(raw []byte) bool {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return false
	}

	var token string
	if err := json.Unmarshal(fields["di_token"], &token); err != nil || token == "" {
		return false
	}
	var refreshToken string
	return json.Unmarshal(fields["di_refresh_token"], &refreshToken) == nil
}

// ImportLegacyTokenFile reads a 0.3.x token store.
//
// path may be the token directory (the GARMINTOKENS form) or the JSON file itself;
// ResolveTokenFilePath applies the same ~username and symlink rules as every other
// path in this package, and the file must be owner-only.
//
// It reports ErrNoTokens when the file is absent, and ErrIncompatibleTokenFile when
// the file exists but is not a 0.3.x document. No error quotes the document.
func ImportLegacyTokenFile(path string) (TokenSet, error) {
	resolved, err := ResolveTokenFilePath(path)
	if err != nil {
		return TokenSet{}, err
	}

	raw, err := readOwnerOnlyFile(resolved, ErrNoTokens)
	if err != nil {
		return TokenSet{}, err
	}
	if !IsLegacyTokenDocument(raw) {
		return TokenSet{}, fmt.Errorf("store: %q is not a 0.3.x token document (source=%s, %d bytes): %w",
			resolved, sourcePath, len(raw), ErrIncompatibleTokenFile)
	}
	return decodeTokenDocument(raw, sourcePath)
}

// ExportLegacyTokenFile writes set in the 0.3.x format and returns the file it
// wrote.
//
// The file is written atomically as mode 0600 inside a mode 0700 directory, like
// every other token file here. An export deliberately puts an unencrypted refresh
// token on disk, so it exists for migration and for interoperability with 0.3.x
// tooling, not as a storage mode.
func ExportLegacyTokenFile(path string, set TokenSet) (string, error) {
	if set.IsZero() {
		return "", fmt.Errorf("store: refusing to export an empty token set: %w", ErrInvalidConfig)
	}
	resolved, err := ResolveTokenFilePath(path)
	if err != nil {
		return "", err
	}
	if err := ensureOwnerOnlyDir(filepath.Dir(resolved)); err != nil {
		return "", err
	}
	if err := writeFileAtomically(resolved, encodeLegacyDocument(set)); err != nil {
		return "", err
	}
	return resolved, nil
}

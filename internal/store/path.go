package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// legacyTokenFileName is the 0.3.x token file name inside a token directory.
//
// Source: token_file_path in client.py (0.3.10).
const legacyTokenFileName = "garmin_tokens.json"

// ResolveTokenFilePath turns a token directory or file path into the token file it
// designates, after refusing every unsafe form.
//
// The rules reproduce the 0.3.10 hardening:
//
//   - a ~username prefix is refused, because it expands into another local
//     account's home directory; bare ~ and ~/... expand to the current user's home
//     and are accepted;
//   - a symlink anywhere in the ancestry is refused, not only in the final
//     component, because an intermediate symlinked directory redirects the whole
//     tree and O_NOFOLLOW cannot see it;
//   - a path that is an existing directory, or whose name does not end in .json,
//     designates <path>/garmin_tokens.json. The suffix test is case-insensitive.
func ResolveTokenFilePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("store: empty token path: %w", ErrInvalidConfig)
	}
	expanded, err := expandHome(path)
	if err != nil {
		return "", err
	}
	if err := checkPathAncestry(expanded); err != nil {
		return "", err
	}

	if isDir(expanded) || !strings.EqualFold(filepath.Ext(expanded), ".json") {
		return filepath.Join(expanded, legacyTokenFileName), nil
	}
	return expanded, nil
}

// expandHome replaces a leading ~ or ~/ with the current user's home directory and
// refuses ~username.
//
// Source: _OTHER_USER_HOME_RE in client.py (0.3.10). The separator test covers
// both / and \, so a Windows-style ~admin\tokens.json is refused too.
func expandHome(path string) (string, error) {
	if !strings.HasPrefix(path, "~") {
		return path, nil
	}
	rest := path[1:]
	if rest != "" && !isPathSeparator(rest[0]) {
		return "", fmt.Errorf("store: token path %q names another user's home: %w", path, ErrForeignHomePath)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("store: resolve home directory for %q: %w", path, ErrInvalidConfig)
	}
	if rest == "" {
		return home, nil
	}
	return filepath.Join(home, filepath.FromSlash(rest[1:])), nil
}

func isPathSeparator(char byte) bool {
	return char == '/' || char == '\\'
}

func isDir(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir()
}

// The symlink ancestry rule lives in internal/securefile; checkPathAncestry in
// secure.go is the entry point.
//
// Source: token_file_path in client.py (0.3.10), which walks Path.parents rather
// than relying on O_NOFOLLOW, which covers the last component only. The whole
// ancestry must be real: on macOS a path under /var is refused, because /var is a
// symlink to /private/var.

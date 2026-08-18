package cryptostore

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateKeyRejectsNonPositiveVersions(t *testing.T) {
	for _, version := range []int{0, -1} {
		if _, err := GenerateKey(version); !errors.Is(err, ErrInvalidKeyVersion) {
			t.Fatalf("GenerateKey(%d) error = %v, want ErrInvalidKeyVersion", version, err)
		}
	}
}

func TestGenerateKeyProducesDistinctMaterial(t *testing.T) {
	first := mustKey(t, 1)
	second := mustKey(t, 1)
	if string(first.bytes()) == string(second.bytes()) {
		t.Fatal("two generated keys share material")
	}
	if len(first.bytes()) != keyLen {
		t.Fatalf("key material is %d bytes, want %d", len(first.bytes()), keyLen)
	}
}

func TestLoadOrCreateKeyCreatesOwnerOnlyMaterial(t *testing.T) {
	dir := filepath.Join(tempDir(t), "config", "keys")

	key, err := LoadOrCreateKey(dir, 3)
	if err != nil {
		t.Fatalf("LoadOrCreateKey: %v", err)
	}
	if key.version != 3 {
		t.Fatalf("key version = %d, want 3", key.version)
	}

	info, err := os.Stat(keyFilePath(dir, 3))
	if err != nil {
		t.Fatalf("stat key file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("key file mode = %04o, want 0600", perm)
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat key dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Fatalf("key dir mode = %04o, want 0700", perm)
	}
}

func TestLoadOrCreateKeyIsIdempotent(t *testing.T) {
	dir := tempDir(t)
	first, err := LoadOrCreateKey(dir, 1)
	if err != nil {
		t.Fatalf("first LoadOrCreateKey: %v", err)
	}
	second, err := LoadOrCreateKey(dir, 1)
	if err != nil {
		t.Fatalf("second LoadOrCreateKey: %v", err)
	}
	if string(first.bytes()) != string(second.bytes()) {
		t.Fatal("LoadOrCreateKey regenerated material for an existing version")
	}
}

// TestLoadOrCreateKeyTightensAWidenedDirectoryOnTheExistingKeyPath is gap 4: the
// existing-key fast path used to return the loaded key without re-checking the
// directory's permissions, so a key directory widened externally (a
// misconfigured deployment step, say) stayed widened until the next key
// installation. EnsureDir's chmod is skipped only when the mode already
// matches exactly, so re-running it on every load is cheap and cannot fail on
// a directory that was already correct.
func TestLoadOrCreateKeyTightensAWidenedDirectoryOnTheExistingKeyPath(t *testing.T) {
	dir := filepath.Join(tempDir(t), "config", "keys")

	if _, err := LoadOrCreateKey(dir, 3); err != nil {
		t.Fatalf("first LoadOrCreateKey: %v", err)
	}
	if err := os.Chmod(dir, 0o777); err != nil { //nolint:gosec // deliberately widened, this is the case under test
		t.Fatalf("widen the key directory: %v", err)
	}

	key, err := LoadOrCreateKey(dir, 3)
	if err != nil {
		t.Fatalf("LoadOrCreateKey on the existing-key path with a widened directory: %v", err)
	}
	if key.version != 3 {
		t.Fatalf("key version = %d, want 3", key.version)
	}

	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat key dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Fatalf("key dir mode = %04o after reload, want 0700 (the existing-key path must still tighten it)", perm)
	}
}

// TestLoadKeyTightensAWidenedDirectoryOnItsOwnDirectPath is the gap the review
// found: TestLoadOrCreateKeyTightensAWidenedDirectoryOnTheExistingKeyPath only
// proved the fast path inside LoadOrCreateKey re-verified the directory. A
// rotated deployment's retired keys, and any diagnostic, load material through
// LoadKey directly rather than through LoadOrCreateKey — internal/cmd's
// loadRetiredKeys and doctor's checkKey both do — so LoadKey itself, not just
// its caller, must catch a directory an operator or a misconfigured mount
// widened after installation.
func TestLoadKeyTightensAWidenedDirectoryOnItsOwnDirectPath(t *testing.T) {
	dir := filepath.Join(tempDir(t), "config", "keys")
	if _, err := LoadOrCreateKey(dir, 1); err != nil {
		t.Fatalf("LoadOrCreateKey: %v", err)
	}
	if err := os.Chmod(dir, 0o777); err != nil { //nolint:gosec // deliberately widened, this is the case under test
		t.Fatalf("widen the key directory: %v", err)
	}

	if _, err := LoadKey(dir, 1); err != nil {
		t.Fatalf("LoadKey on a widened directory this process owns: %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat key dir: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("key dir mode = %04o after LoadKey, want 0700 (LoadKey itself must tighten it)", perm)
	}
}

// TestLoadKeyRefusesAMissingKeyDirectoryWithoutCreatingIt proves the
// directory verification LoadKey now performs never provisions what it did
// not find: a doctor-style caller that must never create the very thing it is
// diagnosing depends on this.
func TestLoadKeyRefusesAMissingKeyDirectoryWithoutCreatingIt(t *testing.T) {
	dir := filepath.Join(tempDir(t), "never-created", "keys")

	if _, err := LoadKey(dir, 1); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("LoadKey on an absent directory: err = %v, want ErrKeyNotFound", err)
	}
	if _, statErr := os.Stat(dir); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("LoadKey created the key directory it was only asked to read: stat err = %v", statErr)
	}
}

func TestLoadKeyRefusesMissingKey(t *testing.T) {
	_, err := LoadKey(tempDir(t), 1)
	if !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("LoadKey error = %v, want ErrKeyNotFound", err)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("LoadKey error = %v, want it to wrap fs.ErrNotExist", err)
	}
}

func plantKeyFile(t *testing.T, dir string, version int, content string, perm fs.FileMode) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := keyFilePath(dir, version)
	if err := os.WriteFile(path, []byte(content), perm); err != nil {
		t.Fatalf("write key file: %v", err)
	}
	if err := os.Chmod(path, perm); err != nil {
		t.Fatalf("chmod key file: %v", err)
	}
	return path
}

func TestLoadKeyRefusesMalformedMaterial(t *testing.T) {
	shortKey := base64.StdEncoding.EncodeToString(make([]byte, 16))
	goodKey := base64.StdEncoding.EncodeToString(make([]byte, keyLen))

	cases := map[string]string{
		"not json":          "this is not json",
		"empty object":      `{}`,
		"bad base64":        `{"version":1,"key":"!!!not base64!!!"}`,
		"short key":         `{"version":1,"key":"` + shortKey + `"}`,
		"version mismatch":  `{"version":9,"key":"` + goodKey + `"}`,
		"zero version":      `{"version":0,"key":"` + goodKey + `"}`,
		"truncated json":    `{"version":1,`,
		"key is not string": `{"version":1,"key":42}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			dir := tempDir(t)
			plantKeyFile(t, dir, 1, content, 0o600)
			if _, err := LoadKey(dir, 1); !errors.Is(err, ErrMalformedKey) {
				t.Fatalf("LoadKey error = %v, want ErrMalformedKey", err)
			}
		})
	}
}

func TestLoadKeyErrorNeverContainsKeyMaterial(t *testing.T) {
	dir := tempDir(t)
	secret := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	plantKeyFile(t, dir, 1, `{"version":1,"key":"`+secret+`"}`, 0o600)

	_, err := LoadKey(dir, 1)
	if err == nil {
		t.Fatal("LoadKey accepted a short key")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("LoadKey error leaked key material: %q", err.Error())
	}
}

func TestLoadKeyAcceptsWellFormedFileWrittenByUs(t *testing.T) {
	dir := tempDir(t)
	created, err := LoadOrCreateKey(dir, 4)
	if err != nil {
		t.Fatalf("LoadOrCreateKey: %v", err)
	}

	raw, err := os.ReadFile(keyFilePath(dir, 4))
	if err != nil {
		t.Fatalf("read key file: %v", err)
	}
	var onDisk struct {
		Version int    `json:"version"`
		Key     string `json:"key"`
	}
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("key file is not JSON: %v", err)
	}
	if onDisk.Version != 4 {
		t.Fatalf("on-disk version = %d, want 4", onDisk.Version)
	}
	decoded, err := base64.StdEncoding.DecodeString(onDisk.Key)
	if err != nil {
		t.Fatalf("on-disk key is not base64: %v", err)
	}
	if len(decoded) != keyLen {
		t.Fatalf("on-disk key is %d bytes, want %d", len(decoded), keyLen)
	}

	loaded, err := LoadKey(dir, 4)
	if err != nil {
		t.Fatalf("LoadKey: %v", err)
	}
	if string(loaded.bytes()) != string(created.bytes()) {
		t.Fatal("LoadKey returned different material than LoadOrCreateKey created")
	}
}

// tempDir returns a temporary directory with every symlink resolved.
// t.TempDir alone is not enough: on macOS it sits under /var, which is a symlink
// to /private/var, and the full-ancestry symlink check correctly refuses that.
// The upstream Python suite gets the same property from pytest's resolved
// tmp_path.
func tempDir(t *testing.T) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	return resolved
}

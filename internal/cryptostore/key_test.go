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

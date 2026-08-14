//go:build unix

package cryptostore

import (
	"encoding/base64"
	"errors"
	"io/fs"
	"os"
	"testing"
)

func validKeyJSON() string {
	return `{"version":1,"key":"` + base64.StdEncoding.EncodeToString(make([]byte, keyLen)) + `"}`
}

// TestLoadKeyRefusesGroupOrWorldAccessibleKey covers the requirement that the
// master key file is owner-only. Key material readable by another local account
// is treated as compromised, not as a warning.
func TestLoadKeyRefusesGroupOrWorldAccessibleKey(t *testing.T) {
	for _, perm := range []fs.FileMode{0o640, 0o604, 0o660, 0o666, 0o644} {
		t.Run(perm.String(), func(t *testing.T) {
			dir := tempDir(t)
			plantKeyFile(t, dir, 1, validKeyJSON(), perm)
			if _, err := LoadKey(dir, 1); !errors.Is(err, ErrInsecureKeyPermissions) {
				t.Fatalf("LoadKey with mode %04o: err = %v, want ErrInsecureKeyPermissions", perm, err)
			}
		})
	}
}

func TestLoadOrCreateKeyRefusesAnExistingPermissiveKey(t *testing.T) {
	dir := tempDir(t)
	plantKeyFile(t, dir, 1, validKeyJSON(), 0o644)

	if _, err := LoadOrCreateKey(dir, 1); !errors.Is(err, ErrInsecureKeyPermissions) {
		t.Fatalf("LoadOrCreateKey err = %v, want ErrInsecureKeyPermissions", err)
	}
}

func TestLoadKeyAcceptsStricterThanOwnerReadWrite(t *testing.T) {
	dir := tempDir(t)
	plantKeyFile(t, dir, 1, validKeyJSON(), 0o400)

	if _, err := LoadKey(dir, 1); err != nil {
		t.Fatalf("LoadKey with mode 0400: %v", err)
	}
}

// TestLoadKeyRefusesSymlinkedKeyFile mirrors the 0.3.10 hardening: a planted
// symlink must not redirect a read of secret material.
func TestLoadKeyRefusesSymlinkedKeyFile(t *testing.T) {
	dir := tempDir(t)
	elsewhere := tempDir(t)
	target := plantKeyFile(t, elsewhere, 2, validKeyJSON(), 0o600)

	if err := os.Symlink(target, keyFilePath(dir, 1)); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if _, err := LoadKey(dir, 1); !errors.Is(err, ErrInsecureKeyPath) {
		t.Fatalf("LoadKey through symlink: err = %v, want ErrInsecureKeyPath", err)
	}
}

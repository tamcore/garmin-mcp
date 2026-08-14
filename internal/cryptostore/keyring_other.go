//go:build !darwin && !linux

package cryptostore

// keyringPlatform names the platform group with no keyring integration. Windows
// lands here: DPAPI or the Credential Manager would be its backend, and neither
// is reachable from the standard library.
func keyringPlatform() string { return "none" }

// keyringAvailable reports false, so the file backend is always used.
func keyringAvailable() bool { return false }

// keyringLoad is the no-op fallback. See keyring.go for why no backend ships.
func keyringLoad(_ string, version int) (Key, error) {
	_ = keyringItem(version)
	return Key{}, errKeyringUnsupported
}

//go:build darwin

package cryptostore

// keyringPlatform names the backend this platform would use: the macOS Keychain,
// through the Security framework.
func keyringPlatform() string { return "darwin-keychain" }

// keyringAvailable reports false: reaching the Keychain needs cgo, which the
// release builds disable, so the file backend is used.
func keyringAvailable() bool { return false }

// keyringLoad is the no-op fallback. See keyring.go for why no backend ships.
func keyringLoad(_ string, version int) (Key, error) {
	_ = keyringItem(version)
	return Key{}, errKeyringUnsupported
}

//go:build linux

package cryptostore

// keyringPlatform names the backend this platform would use: the freedesktop
// Secret Service, reached over D-Bus.
func keyringPlatform() string { return "linux-secret-service" }

// keyringAvailable reports false: a Secret Service client is a dependency this
// repository has not adopted, so the file backend is used.
func keyringAvailable() bool { return false }

// keyringLoad is the no-op fallback. See keyring.go for why no backend ships.
func keyringLoad(_ string, version int) (Key, error) {
	_ = keyringItem(version)
	return Key{}, errKeyringUnsupported
}

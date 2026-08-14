package cryptostore

import "errors"

// The OS keyring is an optional key backend. It is declared here and implemented
// per platform in keyring_darwin.go, keyring_linux.go and keyring_other.go, so
// that:
//
//   - the file backend is always available and is the only shipped backend;
//   - no platform file needs cgo, so CGO_ENABLED=0 cross-compilation keeps
//     working for every release target.
//
// Every platform currently reports errKeyringUnsupported. That is a deliberate
// placeholder, not a working backend: the Keychain and Secret Service APIs need
// either cgo or a D-Bus dependency, and this repository adds neither without an
// ADR. A caller must treat an unsupported keyring as "use the key file", never
// as "start without encryption".

// errKeyringUnsupported means this build has no keyring backend. It is
// unexported: no caller outside this package selects a backend.
var errKeyringUnsupported = errors.New("cryptostore: no OS keyring backend in this build")

// keyringItem names the keyring entry that would hold one key version. It is the
// account name under the service, mirroring the key file name.
func keyringItem(version int) string {
	return keyFilePath("", version)
}

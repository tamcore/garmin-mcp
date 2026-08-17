package store

import (
	"errors"
	"fmt"

	"github.com/tamcore/garmin-mcp/internal/cryptostore"
)

// keySet is the small set of cryptostore keys one store may need at once: exactly
// one active key that seals every new write, plus zero or more retired keys kept
// only to open a record a rotation has not yet re-sealed onto the active key.
//
// This is what makes a mixed-version read window possible during a staged key
// rotation. Every write in this package uses the active key: a retired key is
// never used to seal anything, only to open what it already sealed before the
// rotation began. A read tries the active key first and then every retired key in
// turn, stopping at the one whose declared key version actually matches the
// envelope; a record sealed under a version this set does not hold at all
// exhausts every candidate and reports the last mismatch, which is the same
// ErrCorruptRecord-wrapped failure a single-key store has always returned for the
// wrong key. An unknown key version therefore fails closed with no new sentinel.
type keySet struct {
	active  cryptostore.Key
	retired []cryptostore.Key
}

// newKeySet builds a keySet, probing active the same way a lone key has always
// been probed, so a zero Key is refused before it reaches the first write.
func newKeySet(active cryptostore.Key, retired []cryptostore.Key) (keySet, error) {
	if err := checkKeyUsable(active); err != nil {
		return keySet{}, err
	}
	return keySet{active: active, retired: retired}, nil
}

// candidates lists every key worth trying a decrypt with, active first.
func (k keySet) candidates() []cryptostore.Key {
	out := make([]cryptostore.Key, 0, 1+len(k.retired))
	out = append(out, k.active)
	return append(out, k.retired...)
}

// activeVersion reports the cryptostore version of the active key.
func (k keySet) activeVersion() (int, error) {
	return keyVersionOf(k.active)
}

// encrypt seals under the active key. A retired key never seals anything.
func (k keySet) encrypt(principal, recordType string, plaintext []byte) ([]byte, error) {
	return cryptostore.Encrypt(k.active, principal, recordType, plaintext)
}

// decrypt opens sealed by trying the active key and then every retired key, in
// that order, stopping at the first whose declared key version matches the
// envelope, and reports the key version that opened it.
//
// A version match that still fails authentication is tampering, a wrong key of the
// same version, or a replay under a different principal or record type, and is
// reported immediately: it is never masked by trying another key.
func (k keySet) decrypt(principal, recordType string, sealed []byte) ([]byte, int, error) {
	var lastErr error
	for _, key := range k.candidates() {
		plaintext, err := cryptostore.Decrypt(key, principal, recordType, sealed)
		switch {
		case err == nil:
			version, verr := keyVersionOf(key)
			if verr != nil {
				return nil, 0, verr
			}
			return plaintext, version, nil
		case errors.Is(err, cryptostore.ErrKeyVersionMismatch):
			lastErr = err
			continue
		default:
			return nil, 0, err
		}
	}
	if lastErr == nil {
		lastErr = cryptostore.ErrKeyVersionMismatch
	}
	return nil, 0, fmt.Errorf("cryptostore: no configured key opens this envelope: %w", lastErr)
}

// resealPlan is the outcome of deciding whether one sealed record needs
// re-encrypting onto the active key.
type resealPlan struct {
	sealed  []byte
	changed bool
}

// planReseal opens sealed and, when it was not already sealed under the active
// key, re-encrypts the same plaintext under the active key with the same
// principal and record type binding. It changes nothing when the record already
// matches the active version, which is what makes a rotation resumable: a killed
// run simply re-scans, and a record already at the target version reports
// changed=false and is skipped rather than re-sealed a second time.
func (k keySet) planReseal(principal, recordType string, sealed []byte) (resealPlan, error) {
	plaintext, usedVersion, err := k.decrypt(principal, recordType, sealed)
	if err != nil {
		return resealPlan{}, err
	}
	active, err := k.activeVersion()
	if err != nil {
		return resealPlan{}, err
	}
	if usedVersion == active {
		return resealPlan{sealed: sealed, changed: false}, nil
	}
	fresh, err := k.encrypt(principal, recordType, plaintext)
	if err != nil {
		return resealPlan{}, err
	}
	return resealPlan{sealed: fresh, changed: true}, nil
}

package store

import (
	"errors"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/cryptostore"
)

const (
	keySetPrincipal  = "keyset-test-principal"
	keySetRecordType = "keyset-test-record"
)

func mustGenerateKey(t *testing.T, version int) cryptostore.Key {
	t.Helper()
	key, err := cryptostore.GenerateKey(version)
	if err != nil {
		t.Fatalf("GenerateKey(%d): %v", version, err)
	}
	return key
}

// TestKeySetDecryptFailsClosedOnUnknownVersion is the required property: a record
// sealed under a key version this keySet does not hold at all must never be
// silently accepted. It must report an error, not a wrong plaintext.
func TestKeySetDecryptFailsClosedOnUnknownVersion(t *testing.T) {
	strangerKey := mustGenerateKey(t, 7)
	sealed, err := cryptostore.Encrypt(strangerKey, keySetPrincipal, keySetRecordType, []byte("secret"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	// The set holds neither version 7 (the sealing key) nor any key that happens
	// to open it: active is 1, retired is [2, 3].
	set, err := newKeySet(mustGenerateKey(t, 1), []cryptostore.Key{mustGenerateKey(t, 2), mustGenerateKey(t, 3)})
	if err != nil {
		t.Fatalf("newKeySet: %v", err)
	}

	plaintext, version, err := set.decrypt(keySetPrincipal, keySetRecordType, sealed)
	if err == nil {
		t.Fatalf("decrypt of an unknown key version succeeded with version %d and plaintext %q, want an error",
			version, plaintext)
	}
	if !errors.Is(err, cryptostore.ErrKeyVersionMismatch) {
		t.Fatalf("decrypt error = %v, want it to wrap cryptostore.ErrKeyVersionMismatch", err)
	}
}

// TestKeySetDecryptOpensARecordStillUnderARetiredKey is the mixed-version read
// window: a record sealed before a rotation began must still open once its key is
// listed as retired, without needing to become the active key again.
func TestKeySetDecryptOpensARecordStillUnderARetiredKey(t *testing.T) {
	oldKey := mustGenerateKey(t, 1)
	sealed, err := cryptostore.Encrypt(oldKey, keySetPrincipal, keySetRecordType, []byte("still readable"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	set, err := newKeySet(mustGenerateKey(t, 2), []cryptostore.Key{oldKey})
	if err != nil {
		t.Fatalf("newKeySet: %v", err)
	}

	plaintext, version, err := set.decrypt(keySetPrincipal, keySetRecordType, sealed)
	if err != nil {
		t.Fatalf("decrypt of a record still under a retired key: %v", err)
	}
	if version != 1 {
		t.Fatalf("decrypt reported version %d, want 1 (the retired key that actually opened it)", version)
	}
	if string(plaintext) != "still readable" {
		t.Fatalf("decrypt plaintext = %q, want %q", plaintext, "still readable")
	}
}

// TestKeySetDecryptNeverSilentlyAcceptsTamperingUnderTheActiveVersion checks that a
// version match which fails authentication is reported immediately, never masked
// by falling through to a retired key.
func TestKeySetDecryptNeverSilentlyAcceptsTamperingUnderTheActiveVersion(t *testing.T) {
	active := mustGenerateKey(t, 2)
	sealed, err := cryptostore.Encrypt(active, keySetPrincipal, keySetRecordType, []byte("value"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	tampered := append([]byte(nil), sealed...)
	tampered[len(tampered)-1] ^= 0xFF

	set, err := newKeySet(active, []cryptostore.Key{mustGenerateKey(t, 1)})
	if err != nil {
		t.Fatalf("newKeySet: %v", err)
	}

	_, _, err = set.decrypt(keySetPrincipal, keySetRecordType, tampered)
	if !errors.Is(err, cryptostore.ErrAuthentication) {
		t.Fatalf("decrypt of tampered ciphertext = %v, want ErrAuthentication", err)
	}
}

// TestKeySetEncryptAlwaysUsesTheActiveKey documents that a retired key never seals
// anything: every write goes onto the active version regardless of how many
// retired keys are configured.
func TestKeySetEncryptAlwaysUsesTheActiveKey(t *testing.T) {
	active := mustGenerateKey(t, 3)
	set, err := newKeySet(active, []cryptostore.Key{mustGenerateKey(t, 1), mustGenerateKey(t, 2)})
	if err != nil {
		t.Fatalf("newKeySet: %v", err)
	}

	sealed, err := set.encrypt(keySetPrincipal, keySetRecordType, []byte("fresh"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	plaintext, version, err := set.decrypt(keySetPrincipal, keySetRecordType, sealed)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if version != 3 {
		t.Fatalf("a fresh write was sealed under version %d, want the active version 3", version)
	}
	if string(plaintext) != "fresh" {
		t.Fatalf("plaintext = %q, want %q", plaintext, "fresh")
	}
}

// TestPlanResealSkipsARecordAlreadyOnTheActiveKey is the no-op path a resumed,
// already-completed reseal must take: a record already sealed under the active
// key reports changed=false, and the bytes it returns are the ones it was given,
// not a fresh re-encryption.
func TestPlanResealSkipsARecordAlreadyOnTheActiveKey(t *testing.T) {
	active := mustGenerateKey(t, 5)
	set, err := newKeySet(active, nil)
	if err != nil {
		t.Fatalf("newKeySet: %v", err)
	}
	sealed, err := set.encrypt(keySetPrincipal, keySetRecordType, []byte("value"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	plan, err := set.planReseal(keySetPrincipal, keySetRecordType, sealed)
	if err != nil {
		t.Fatalf("planReseal: %v", err)
	}
	if plan.changed {
		t.Fatal("planReseal reported changed=true for a record already on the active key")
	}
	if string(plan.sealed) != string(sealed) {
		t.Fatal("planReseal returned different bytes for a record it should have left untouched")
	}
}

// TestPlanResealReencryptsARecordUnderARetiredKey is the actual migration step: a
// record under a retired key is re-encrypted onto the active key, with the same
// plaintext recoverable afterward under the active key alone.
func TestPlanResealReencryptsARecordUnderARetiredKey(t *testing.T) {
	oldKey := mustGenerateKey(t, 1)
	sealed, err := cryptostore.Encrypt(oldKey, keySetPrincipal, keySetRecordType, []byte("migrate me"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	set, err := newKeySet(mustGenerateKey(t, 2), []cryptostore.Key{oldKey})
	if err != nil {
		t.Fatalf("newKeySet: %v", err)
	}

	plan, err := set.planReseal(keySetPrincipal, keySetRecordType, sealed)
	if err != nil {
		t.Fatalf("planReseal: %v", err)
	}
	if !plan.changed {
		t.Fatal("planReseal reported changed=false for a record under a retired key")
	}

	// The record must now be readable by the active key alone, with no retired
	// key configured at all.
	activeOnly, err := newKeySet(set.active, nil)
	if err != nil {
		t.Fatalf("newKeySet: %v", err)
	}
	plaintext, version, err := activeOnly.decrypt(keySetPrincipal, keySetRecordType, plan.sealed)
	if err != nil {
		t.Fatalf("decrypt of the resealed record with no retired key: %v", err)
	}
	if version != 2 {
		t.Fatalf("resealed record opened at version %d, want 2", version)
	}
	if string(plaintext) != "migrate me" {
		t.Fatalf("plaintext = %q, want %q", plaintext, "migrate me")
	}
}

package cryptostore

import (
	"bytes"
	"errors"
	"testing"
)

const (
	testPrincipal  = "8f1c0f52-0f4b-4f2e-9a3a-7f2b1d6c5e40"
	testRecordType = "garmin_di_tokens"
)

func mustKey(t *testing.T, version int) Key {
	t.Helper()
	key, err := GenerateKey(version)
	if err != nil {
		t.Fatalf("GenerateKey(%d): %v", version, err)
	}
	return key
}

func TestEncryptDecryptRoundTripsPlaintext(t *testing.T) {
	key := mustKey(t, 1)
	plaintext := []byte(`{"di_token":"header.body.sig"}`)

	sealed, err := Encrypt(key, testPrincipal, testRecordType, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Contains(sealed, plaintext) {
		t.Fatal("sealed envelope contains the plaintext")
	}

	got, err := Decrypt(key, testPrincipal, testRecordType, sealed)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("Decrypt returned %q, want %q", got, plaintext)
	}
}

func TestEncryptUsesAFreshNoncePerCall(t *testing.T) {
	key := mustKey(t, 1)
	plaintext := []byte("same plaintext")

	first, err := Encrypt(key, testPrincipal, testRecordType, plaintext)
	if err != nil {
		t.Fatalf("Encrypt first: %v", err)
	}
	second, err := Encrypt(key, testPrincipal, testRecordType, plaintext)
	if err != nil {
		t.Fatalf("Encrypt second: %v", err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("two Encrypt calls produced identical envelopes, so the nonce is not random")
	}
}

func TestDecryptRejectsSwappedAdditionalData(t *testing.T) {
	key := mustKey(t, 1)
	sealed, err := Encrypt(key, testPrincipal, testRecordType, []byte("secret"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	cases := []struct {
		name       string
		principal  string
		recordType string
	}{
		{"other principal", "00000000-0000-0000-0000-000000000000", testRecordType},
		{"other record type", testPrincipal, "oauth_refresh_token"},
		{"both swapped", "00000000-0000-0000-0000-000000000000", "oauth_refresh_token"},
		{"empty principal", "", testRecordType},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Decrypt(key, tc.principal, tc.recordType, sealed); !errors.Is(err, ErrAuthentication) {
				t.Fatalf("Decrypt error = %v, want ErrAuthentication", err)
			}
		})
	}
}

func TestDecryptRejectsTamperedEnvelope(t *testing.T) {
	key := mustKey(t, 1)
	sealed, err := Encrypt(key, testPrincipal, testRecordType, []byte("secret payload"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	positions := map[string]int{
		"nonce":      headerLen + 1,
		"ciphertext": headerLen + nonceLen + 1,
		"tag":        len(sealed) - 1,
	}
	for name, index := range positions {
		t.Run(name, func(t *testing.T) {
			tampered := bytes.Clone(sealed)
			tampered[index] ^= 0x01
			if _, err := Decrypt(key, testPrincipal, testRecordType, tampered); !errors.Is(err, ErrAuthentication) {
				t.Fatalf("Decrypt error = %v, want ErrAuthentication", err)
			}
		})
	}
}

func TestDecryptRejectsWrongKeyOfSameVersion(t *testing.T) {
	sealed, err := Encrypt(mustKey(t, 1), testPrincipal, testRecordType, []byte("secret"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := Decrypt(mustKey(t, 1), testPrincipal, testRecordType, sealed); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("Decrypt error = %v, want ErrAuthentication", err)
	}
}

func TestDecryptReportsKeyVersionMismatchBeforeOpening(t *testing.T) {
	sealed, err := Encrypt(mustKey(t, 1), testPrincipal, testRecordType, []byte("secret"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := Decrypt(mustKey(t, 2), testPrincipal, testRecordType, sealed); !errors.Is(err, ErrKeyVersionMismatch) {
		t.Fatalf("Decrypt error = %v, want ErrKeyVersionMismatch", err)
	}
}

func TestDecryptRejectsMalformedEnvelope(t *testing.T) {
	key := mustKey(t, 1)
	cases := map[string][]byte{
		"empty":                nil,
		"truncated header":     {envelopeFormatVersion, 0, 0},
		"truncated nonce":      append([]byte{envelopeFormatVersion, 0, 0, 0, 1}, 1, 2, 3),
		"unknown format":       append([]byte{0xFE, 0, 0, 0, 1}, make([]byte, nonceLen+16)...),
		"no ciphertext at all": append([]byte{envelopeFormatVersion, 0, 0, 0, 1}, make([]byte, nonceLen)...),
	}
	for name, sealed := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Decrypt(key, testPrincipal, testRecordType, sealed); !errors.Is(err, ErrMalformedEnvelope) {
				t.Fatalf("Decrypt error = %v, want ErrMalformedEnvelope", err)
			}
		})
	}
}

// TestStagedRotationReencryptsRecords exercises the documented rotation path:
// read every record with key version N, write it back under version N+1.
func TestStagedRotationReencryptsRecords(t *testing.T) {
	dir := tempDir(t)
	oldKey, err := LoadOrCreateKey(dir, 1)
	if err != nil {
		t.Fatalf("LoadOrCreateKey v1: %v", err)
	}
	plaintext := []byte("rotate me")
	sealedV1, err := Encrypt(oldKey, testPrincipal, testRecordType, plaintext)
	if err != nil {
		t.Fatalf("Encrypt v1: %v", err)
	}

	newKey, err := LoadOrCreateKey(dir, 2)
	if err != nil {
		t.Fatalf("LoadOrCreateKey v2: %v", err)
	}

	// Staged rotation: both versions stay loadable, so records migrate lazily.
	recovered, err := Decrypt(oldKey, testPrincipal, testRecordType, sealedV1)
	if err != nil {
		t.Fatalf("Decrypt with retained v1 key: %v", err)
	}
	sealedV2, err := Encrypt(newKey, testPrincipal, testRecordType, recovered)
	if err != nil {
		t.Fatalf("Encrypt v2: %v", err)
	}
	got, err := Decrypt(newKey, testPrincipal, testRecordType, sealedV2)
	if err != nil {
		t.Fatalf("Decrypt v2: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("rotated record = %q, want %q", got, plaintext)
	}
	if _, err := Decrypt(newKey, testPrincipal, testRecordType, sealedV1); !errors.Is(err, ErrKeyVersionMismatch) {
		t.Fatalf("v1 envelope under v2 key: err = %v, want ErrKeyVersionMismatch", err)
	}

	// The old key file survives rotation, so a partially migrated store still reads.
	reloaded, err := LoadKey(dir, 1)
	if err != nil {
		t.Fatalf("LoadKey v1 after rotation: %v", err)
	}
	if _, err := Decrypt(reloaded, testPrincipal, testRecordType, sealedV1); err != nil {
		t.Fatalf("Decrypt v1 envelope with reloaded v1 key: %v", err)
	}
}

func TestEnvelopeExposesKeyVersionForFutureRotation(t *testing.T) {
	sealed, err := Encrypt(mustKey(t, 7), testPrincipal, testRecordType, []byte("x"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	env, err := parseEnvelope(sealed)
	if err != nil {
		t.Fatalf("parseEnvelope: %v", err)
	}
	if env.keyVersion != 7 {
		t.Fatalf("keyVersion = %d, want 7", env.keyVersion)
	}
	if env.formatVersion != envelopeFormatVersion {
		t.Fatalf("formatVersion = %d, want %d", env.formatVersion, envelopeFormatVersion)
	}
}

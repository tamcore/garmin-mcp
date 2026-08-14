package oauthserver

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
)

const testVerifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk-verifier-padding"

// testChallenge is the S256 challenge for testVerifier, computed the way a client
// computes it rather than the way the server verifies it.
func testChallenge() string {
	sum := sha256.Sum256([]byte(testVerifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func mustChallenge(t *testing.T) CodeChallenge {
	t.Helper()
	challenge, err := ParseCodeChallenge(string(MethodS256), testChallenge())
	if err != nil {
		t.Fatalf("ParseCodeChallenge: %v", err)
	}
	return challenge
}

func TestParseCodeChallengeAcceptsOnlyS256(t *testing.T) {
	challenge := mustChallenge(t)

	if challenge.Method() != MethodS256 {
		t.Fatalf("Method() = %q, want S256", challenge.Method())
	}
	if challenge.Value() != testChallenge() {
		t.Fatal("Value did not round-trip")
	}
}

func TestParseCodeChallengeRefusesDowngradeAndMalformedInput(t *testing.T) {
	valid := testChallenge()

	for name, tc := range map[string]struct{ method, value string }{
		"the plain method":             {challengeMethodPlain, testVerifier},
		"no method":                    {"", valid},
		"lowercase s256":               {"s256", valid},
		"unknown method":               {"S512", valid},
		"no value":                     {string(MethodS256), ""},
		"short value":                  {string(MethodS256), strings.Repeat("a", 42)},
		"challenge over 43 characters": {string(MethodS256), strings.Repeat("a", 44)},
		"padded base64": {
			string(MethodS256),
			base64.StdEncoding.EncodeToString([]byte(strings.Repeat("a", 32))),
		},
		"non-base64url":    {string(MethodS256), strings.Repeat("+", 43)},
		"whitespace value": {string(MethodS256), strings.Repeat("a", 42) + " "},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseCodeChallenge(tc.method, tc.value)
			if !errors.Is(err, ErrInvalidCodeChallenge) {
				t.Fatalf("ParseCodeChallenge(%q, ...) error = %v, want ErrInvalidCodeChallenge",
					tc.method, err)
			}
		})
	}
}

func TestCodeChallengeVerifyAcceptsTheMatchingVerifier(t *testing.T) {
	if err := mustChallenge(t).Verify(testVerifier); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestCodeChallengeVerifyRejectsMismatchAndMalformedVerifiers(t *testing.T) {
	challenge := mustChallenge(t)

	for name, tc := range map[string]struct {
		verifier string
		want     error
	}{
		"wrong verifier":               {testVerifier + "x", ErrPKCEVerificationFailed},
		"no verifier":                  {"", ErrInvalidCodeVerifier},
		"too short":                    {strings.Repeat("a", 42), ErrInvalidCodeVerifier},
		"verifier over 128 characters": {strings.Repeat("a", 129), ErrInvalidCodeVerifier},
		"reserved char":                {strings.Repeat("a", 42) + "%", ErrInvalidCodeVerifier},
		"space":                        {strings.Repeat("a", 42) + " ", ErrInvalidCodeVerifier},
		"the challenge":                {challenge.Value(), ErrPKCEVerificationFailed},
		"unset challenge":              {testVerifier, ErrInvalidCodeChallenge},
		"control in place":             {strings.Repeat("a", 42) + "\x00", ErrInvalidCodeVerifier},
	} {
		t.Run(name, func(t *testing.T) {
			subject := challenge
			if name == "unset challenge" {
				subject = CodeChallenge{}
			}
			if err := subject.Verify(tc.verifier); !errors.Is(err, tc.want) {
				t.Fatalf("Verify error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestCodeChallengeErrorsAndRenderingsCarryNoVerifier(t *testing.T) {
	challenge := mustChallenge(t)
	err := challenge.Verify(testVerifier + "x")

	rendered := []string{
		err.Error(),
		challenge.String(),
		challenge.GoString(),
		fmt.Sprintf("%v", challenge),
		fmt.Sprintf("%#v", challenge),
	}
	for _, text := range rendered {
		if strings.Contains(text, testVerifier) {
			t.Fatalf("rendering leaked the code verifier: %q", text)
		}
		if strings.Contains(text, challenge.Value()) {
			t.Fatalf("rendering leaked the code challenge: %q", text)
		}
	}
}

func TestCodeChallengeIsZero(t *testing.T) {
	if !(CodeChallenge{}).IsZero() {
		t.Fatal("the zero CodeChallenge does not report IsZero")
	}
	if mustChallenge(t).IsZero() {
		t.Fatal("a parsed CodeChallenge reports IsZero")
	}
}

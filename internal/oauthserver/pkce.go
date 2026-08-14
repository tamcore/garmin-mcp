package oauthserver

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
)

// PKCE bounds and the one accepted method.
const (
	// MethodS256 is the only code challenge method this server accepts. RFC 7636
	// §4.2 also defines "plain"; accepting it would let an attacker who can read
	// the authorization request replay the code, so it is refused everywhere,
	// including as a downgrade on an otherwise valid request.
	MethodS256 ChallengeMethod = "S256"

	// challengeMethodPlain is the RFC 7636 §4.2 method this server never accepts. It is
	// named so the refusal is explicit in the code and in the tests, rather than being
	// an unnamed string that happens not to equal S256.
	challengeMethodPlain = "plain"

	// challengeLen is the length of a base64url-encoded SHA-256 digest without
	// padding.
	challengeLen = 43

	// MinCodeVerifierLen and MaxCodeVerifierLen are the RFC 7636 §4.1 bounds.
	MinCodeVerifierLen = 43
	MaxCodeVerifierLen = 128
)

// A ChallengeMethod is a PKCE code challenge method. Only [MethodS256] exists.
type ChallengeMethod string

// challengeValue holds the challenge behind a pointer for the same reason
// secretMaterial does: a caller that strips CodeChallenge's methods with a type
// conversion gets an address, not the challenge.
type challengeValue struct {
	value string
}

// A CodeChallenge is a validated PKCE S256 code challenge.
//
// The challenge is not itself a credential — it is the digest of one — but it is
// still kept out of every rendering path, because a challenge in a log line next
// to a code is a step towards correlating the two.
//
// The zero CodeChallenge is invalid: Verify on it fails rather than succeeding
// vacuously, which is what makes "no PKCE" impossible to confuse with "PKCE
// satisfied".
type CodeChallenge struct {
	v *challengeValue
}

// ParseCodeChallenge validates the code_challenge_method and code_challenge pair
// from an authorization request. The method must be exactly "S256"; the challenge
// must be exactly 43 unpadded base64url characters, which is the only shape a
// SHA-256 digest can have.
func ParseCodeChallenge(method, value string) (CodeChallenge, error) {
	if ChallengeMethod(method) != MethodS256 {
		return CodeChallenge{}, fmt.Errorf(
			"code challenge method must be %q, this server never accepts plain: %w",
			MethodS256, ErrInvalidCodeChallenge)
	}
	if len(value) != challengeLen {
		return CodeChallenge{}, fmt.Errorf("code challenge is %d characters, want %d: %w",
			len(value), challengeLen, ErrInvalidCodeChallenge)
	}
	if _, err := base64.RawURLEncoding.DecodeString(value); err != nil {
		return CodeChallenge{}, fmt.Errorf(
			"code challenge is not unpadded base64url: %w", ErrInvalidCodeChallenge)
	}
	return CodeChallenge{v: &challengeValue{value: value}}, nil
}

// Method reports the challenge method, which is always [MethodS256] for a
// non-zero CodeChallenge.
func (c CodeChallenge) Method() ChallengeMethod {
	if c.IsZero() {
		return ""
	}
	return MethodS256
}

// Value returns the challenge, for the storage adapter that has to persist it.
func (c CodeChallenge) Value() string {
	if c.v == nil {
		return ""
	}
	return c.v.value
}

// IsZero reports whether c carries no challenge.
func (c CodeChallenge) IsZero() bool { return c.v == nil || c.v.value == "" }

// Verify reports whether verifier is the preimage of c under S256.
//
// It returns ErrInvalidCodeChallenge when c is the zero value, so a code that
// somehow reached the token endpoint without a bound challenge is refused rather
// than accepted; ErrInvalidCodeVerifier when the verifier breaks the RFC 7636
// §4.1 grammar; and ErrPKCEVerificationFailed when a well-formed verifier does
// not match. The comparison is constant time, and neither the verifier nor the
// challenge appears in any of those errors.
func (c CodeChallenge) Verify(verifier string) error {
	if c.IsZero() {
		return fmt.Errorf("no code challenge is bound: %w", ErrInvalidCodeChallenge)
	}
	if err := validateCodeVerifier(verifier); err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(verifier))
	computed := base64.RawURLEncoding.EncodeToString(sum[:])
	if subtle.ConstantTimeCompare([]byte(computed), []byte(c.v.value)) != 1 {
		return fmt.Errorf("verifier does not match the bound challenge: %w",
			ErrPKCEVerificationFailed)
	}
	return nil
}

// validateCodeVerifier applies the RFC 7636 §4.1 grammar: 43 to 128 characters
// drawn from the unreserved set ALPHA / DIGIT / "-" / "." / "_" / "~".
func validateCodeVerifier(verifier string) error {
	if len(verifier) < MinCodeVerifierLen || len(verifier) > MaxCodeVerifierLen {
		return fmt.Errorf("code verifier is %d characters, want %d to %d: %w",
			len(verifier), MinCodeVerifierLen, MaxCodeVerifierLen, ErrInvalidCodeVerifier)
	}
	for i := range len(verifier) {
		if !isUnreserved(verifier[i]) {
			return fmt.Errorf("code verifier carries a disallowed byte at offset %d: %w",
				i, ErrInvalidCodeVerifier)
		}
	}
	return nil
}

func isUnreserved(b byte) bool {
	switch {
	case b >= 'A' && b <= 'Z', b >= 'a' && b <= 'z', b >= '0' && b <= '9':
		return true
	case b == '-', b == '.', b == '_', b == '~':
		return true
	default:
		return false
	}
}

// String reports the method, never the challenge.
func (c CodeChallenge) String() string {
	if c.IsZero() {
		return "oauthserver.CodeChallenge{method:none}"
	}
	return "oauthserver.CodeChallenge{method:" + string(MethodS256) + " value:redacted}"
}

// GoString satisfies the %#v verb with the same redacted rendering.
func (c CodeChallenge) GoString() string { return c.String() }

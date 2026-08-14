package protocol

import (
	"strings"
	"unicode"
)

// Length bounds for the values lifted out of a Garmin response. They keep a
// hostile or malformed payload from inflating logs and error strings.
const (
	// MaxPageTitleLen bounds the HTML <title> kept for diagnostics.
	MaxPageTitleLen = 120
	// MaxServiceTicketLen bounds an accepted CAS service ticket.
	MaxServiceTicketLen = 512
	// MaxMFAMethodLen bounds the reported MFA delivery method.
	MaxMFAMethodLen = 32
	// MaxQueryKeyLen bounds a query parameter name kept in a redacted URL.
	MaxQueryKeyLen = 64
	// MaxURLOpLen bounds the HTTP verb carried by a *url.Error.
	MaxURLOpLen = 16
	// MaxMediaTypeLen bounds a rendered Content-Type media type.
	MaxMediaTypeLen = 64
)

// sanitizeToken keeps only characters valid in a Garmin identifier token and
// bounds the result. An out-of-charset character ends the token, so a payload
// cannot smuggle markup, newlines or separators into a log line.
func sanitizeToken(value string, maxLen int) string {
	trimmed := strings.TrimSpace(value)

	var b strings.Builder
	for _, r := range trimmed {
		if !isTokenRune(r) {
			break
		}
		if b.Len() >= maxLen {
			return ""
		}
		b.WriteRune(r)
	}
	return b.String()
}

func isTokenRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	case r == '-', r == '_', r == '.', r == '~':
		return true
	default:
		return false
	}
}

// sanitizeMediaType keeps the media type of a Content-Type header, dropping any
// parameters and anything outside the token charset plus "/".
func sanitizeMediaType(value string) string {
	mediaType, _, _ := strings.Cut(value, ";")

	var b strings.Builder
	for _, r := range strings.TrimSpace(mediaType) {
		if !isTokenRune(r) && r != '/' && r != '+' {
			break
		}
		if b.Len() >= MaxMediaTypeLen {
			break
		}
		b.WriteRune(r)
	}
	return b.String()
}

// sanitizeTitle drops control characters, collapses runs of whitespace to a
// single space and bounds the length to MaxPageTitleLen runes.
func sanitizeTitle(value string) string {
	var b strings.Builder
	pendingSpace := false
	written := 0

	for _, r := range value {
		switch {
		// Whitespace is tested first: tab and newline are also control runes.
		case unicode.IsSpace(r):
			pendingSpace = b.Len() > 0
			continue
		case unicode.IsControl(r):
			continue
		}
		if pendingSpace && written < MaxPageTitleLen {
			b.WriteRune(' ')
			written++
			pendingSpace = false
		}
		if written >= MaxPageTitleLen {
			break
		}
		b.WriteRune(r)
		written++
	}
	return b.String()
}

// MFA delivery methods this package names in a rendered form. Source: the
// mfaMethod values Garmin's widget emits, which _widget_request_mfa_code acts on
// for "email" and "sms", plus the customerMfaInfo.mfaLastMethodUsed values seen
// on the JSON login APIs.
const (
	mfaMethodSMS           = "sms"
	mfaMethodTOTP          = "totp"
	mfaMethodAuthenticator = "authenticator"
	mfaMethodVoice         = "voice"
)

var knownMFAMethods = [...]string{
	MFAMethodEmail, mfaMethodSMS, mfaMethodTOTP, mfaMethodAuthenticator, mfaMethodVoice,
}

// knownMFAMethod folds an MFA method to a recognized value, or "other". An
// unrecognized value is server-controlled, so it is never echoed.
func knownMFAMethod(value string) string {
	if value == "" {
		return ""
	}
	folded := strings.ToLower(sanitizeToken(value, MaxMFAMethodLen))
	for _, known := range knownMFAMethods {
		if folded == known {
			return known
		}
	}
	return labelOther
}

// knownResponseStatusType folds a responseStatus.type to a recognized value, or
// "other". Diagnostics keep the raw token in Classification.ResponseStatusType;
// only recognized values reach a rendered form.
func knownResponseStatusType(value string) string {
	if value == "" {
		return ""
	}
	if _, ok := knownStatusType(value); ok {
		return value
	}
	return labelOther
}

// mfaMethodOrDefault sanitizes the reported MFA delivery method, falling back to
// MFAMethodEmail. Source: the "email" default for mfaLastMethodUsed.
func mfaMethodOrDefault(value string) string {
	if method := sanitizeToken(value, MaxMFAMethodLen); method != "" {
		return method
	}
	return MFAMethodEmail
}

// containsWordPhrase reports whether phrase occurs in text delimited by
// non-alphanumeric runes on both sides. Substring matching would read "unlocked"
// as "locked" and "invalidated" as "invalid", which then stops the login
// strategy chain on a healthy account.
//
// Both arguments must already be lowercased.
func containsWordPhrase(text, phrase string) bool {
	if phrase == "" {
		return false
	}

	runes := []rune(text)
	target := []rune(phrase)
	for offset := 0; offset+len(target) <= len(runes); offset++ {
		if string(runes[offset:offset+len(target)]) != phrase {
			continue
		}
		if isWordBoundary(runes, offset-1) && isWordBoundary(runes, offset+len(target)) {
			return true
		}
	}
	return false
}

// isWordBoundary reports whether the rune at index is absent or not alphanumeric.
func isWordBoundary(runes []rune, index int) bool {
	if index < 0 || index >= len(runes) {
		return true
	}
	r := runes[index]
	return !unicode.IsLetter(r) && !unicode.IsDigit(r)
}

// containsAnyWordPhrase reports whether any phrase occurs as a delimited word.
func containsAnyWordPhrase(text string, phrases ...string) bool {
	for _, phrase := range phrases {
		if containsWordPhrase(text, phrase) {
			return true
		}
	}
	return false
}

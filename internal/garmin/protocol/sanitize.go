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

// mfaMethodOrDefault sanitizes the reported MFA delivery method, falling back to
// MFAMethodEmail. Source: the "email" default for mfaLastMethodUsed.
func mfaMethodOrDefault(value string) string {
	if method := sanitizeToken(value, MaxMFAMethodLen); method != "" {
		return method
	}
	return MFAMethodEmail
}

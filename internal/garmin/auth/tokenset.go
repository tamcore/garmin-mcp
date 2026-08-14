package auth

import (
	"encoding/json"
	"log/slog"
	"slices"
	"strconv"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
)

// TokenSet is the native 0.3.x DI token set: di_token, di_refresh_token,
// di_client_id and the expiry read from the token's unverified exp claim.
//
// It is secret-bearing. The DI token and the refresh token are bearer
// credentials, so none of the material is reachable as a field: the fields are
// unexported and sit behind a pointer, so a reflective logger, a direct field
// print and a method-stripping alias (type Raw auth.TokenSet) all see an address
// rather than the material. String, GoString, MarshalJSON and LogValue report
// presence and expiry rather than content; the accessors hand the real values to
// a caller that asks for them deliberately.
//
// The value is immutable: WithRotated returns a new set.
//
// The zero value is inert: every accessor reports its zero result and IsZero is
// true.
type TokenSet struct {
	// secrets is a pointer on purpose: it keeps the material one indirection
	// away from reflection. It is never mutated after construction.
	secrets *tokenSecrets
}

// tokenSecrets is the sealed content of a TokenSet.
type tokenSecrets struct {
	token        string
	refreshToken string
	clientID     string
	expiresAt    time.Time
}

// NewTokenSet seals a DI token set. expiresAt is scheduling metadata read from
// an unverified exp claim: see UnverifiedExpiry. It must never authorize
// anything.
func NewTokenSet(token, refreshToken, clientID string, expiresAt time.Time) TokenSet {
	return TokenSet{secrets: &tokenSecrets{
		token:        token,
		refreshToken: refreshToken,
		clientID:     clientID,
		expiresAt:    expiresAt,
	}}
}

// s returns a copy of the sealed content, or the zero content for a zero value.
func (t TokenSet) s() tokenSecrets {
	if t.secrets == nil {
		return tokenSecrets{}
	}
	return *t.secrets
}

// Token is the DI bearer token (di_token). It is a credential: never put it in a
// log line or an error message.
func (t TokenSet) Token() string { return t.s().token }

// RefreshToken is the DI refresh token (di_refresh_token). It is a credential
// and it rotates: persist the rotated value with a compare-and-set.
func (t TokenSet) RefreshToken() string { return t.s().refreshToken }

// ClientID is the DI OAuth2 client id (di_client_id) the token belongs to.
func (t TokenSet) ClientID() string { return t.s().clientID }

// ExpiresAt is the instant the DI token stops being useful, taken from its
// unverified exp claim. It is scheduling metadata only, never an authorization
// input, and a zero value means "unknown", not "expired".
func (t TokenSet) ExpiresAt() time.Time { return t.s().expiresAt }

// IsZero reports whether the set carries no token at all.
func (t TokenSet) IsZero() bool { return t.secrets == nil || t.s().token == "" }

// WithRotated returns a copy of t carrying a refreshed token. An empty
// refreshToken keeps the current one, because Garmin's refresh response may omit
// it. Source: the data.get("refresh_token", self.di_refresh_token) fallback in
// Client._refresh_di_token (client.py, 0.3.10). The receiver is not modified.
func (t TokenSet) WithRotated(token, refreshToken string, expiresAt time.Time) TokenSet {
	next := t.s()
	next.token = token
	next.expiresAt = expiresAt
	if refreshToken != "" {
		next.refreshToken = refreshToken
	}
	return TokenSet{secrets: &next}
}

// WithClientID returns a copy of t whose client id is clientID. The receiver is
// not modified.
func (t TokenSet) WithClientID(clientID string) TokenSet {
	next := t.s()
	next.clientID = clientID
	return TokenSet{secrets: &next}
}

// ExpiresWithin reports whether the token expires at or before now plus window.
// An unknown expiry (the zero instant) is reported as expiring, so a caller
// refreshes rather than trusting an unparsable token indefinitely.
func (t TokenSet) ExpiresWithin(now time.Time, window time.Duration) bool {
	expiry := t.s().expiresAt
	if expiry.IsZero() {
		return true
	}
	return !now.Add(window).Before(expiry)
}

// redactedTokenSet is the only shape a TokenSet is ever rendered or serialized
// in. It reports presence and expiry, never token material.
type redactedTokenSet struct {
	Type            string `json:"type"`
	HasToken        bool   `json:"tokenPresent"`
	HasRefreshToken bool   `json:"refreshTokenPresent"`
	ClientID        string `json:"clientId,omitempty"`
	ExpiresAtUnix   int64  `json:"expiresAtUnix,omitempty"`
}

func (t TokenSet) redacted() redactedTokenSet {
	secrets := t.s()
	out := redactedTokenSet{
		Type:            "auth.TokenSet",
		HasToken:        secrets.token != "",
		HasRefreshToken: secrets.refreshToken != "",
		ClientID:        knownDIClientID(secrets.clientID),
	}
	if !secrets.expiresAt.IsZero() {
		out.ExpiresAtUnix = secrets.expiresAt.Unix()
	}
	return out
}

// knownDIClientID renders a client id only when it is one of the candidate DI
// client ids. Garmin controls the client_id claim, so an unrecognized value is
// reported as "unknown" rather than echoed into a log line.
func knownDIClientID(value string) string {
	if value == "" {
		return ""
	}
	if slices.Contains(protocol.DIClientIDs(), value) {
		return value
	}
	return labelUnknown
}

// String renders a TokenSet without its token or refresh token.
func (t TokenSet) String() string {
	red := t.redacted()
	return "auth.TokenSet{token:" + presence(red.HasToken) +
		" refreshToken:" + presence(red.HasRefreshToken) +
		" clientId:" + quoteLabel(red.ClientID) +
		" expiresAtUnix:" + strconv.FormatInt(red.ExpiresAtUnix, 10) + "}"
}

// GoString satisfies the %#v verb with the same redacted rendering.
func (t TokenSet) GoString() string { return t.String() }

// MarshalJSON serializes the redacted form, so a TokenSet embedded in a log
// record cannot leak its tokens.
func (t TokenSet) MarshalJSON() ([]byte, error) { return json.Marshal(t.redacted()) }

// LogValue implements slog.LogValuer, so structured logging is safe by default.
func (t TokenSet) LogValue() slog.Value {
	red := t.redacted()
	return slog.GroupValue(
		slog.String("type", red.Type),
		slog.Bool("tokenPresent", red.HasToken),
		slog.Bool("refreshTokenPresent", red.HasRefreshToken),
		slog.String("clientId", red.ClientID),
		slog.Int64("expiresAtUnix", red.ExpiresAtUnix),
	)
}

func presence(present bool) string {
	if present {
		return "present"
	}
	return "absent"
}

func quoteLabel(value string) string {
	if value == "" {
		return `""`
	}
	return `"` + value + `"`
}

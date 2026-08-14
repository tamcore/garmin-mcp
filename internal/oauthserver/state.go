package oauthserver

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
)

// MaxClientStateLen bounds the client's opaque state parameter. RFC 6749 puts no
// limit on it, so this server does: the value is echoed into a redirect URL and
// held in a server-side transaction, and neither may be unbounded.
const MaxClientStateLen = 1024

// stateString is the state under its own type, so the field that holds it can be
// a pointer.
type stateString string

// stateMaterial holds the state behind a second pointer, so a method-stripping
// alias cannot reach it through fmt's badVerb path. secret.go explains the shape.
type stateMaterial struct {
	value *stateString
}

// A ClientState is the client's opaque state parameter.
//
// It is preserved byte for byte and returned unchanged in the authorization
// response, which is what lets the client bind the response to its own request.
// This server never uses it for its own purposes: the transaction capability, the
// browser cookie and the form CSRF token are independent server-generated values,
// because a value the client chose cannot protect the server against the client.
//
// It is redacted from every rendering path. State is attacker-influenced input
// that clients routinely use to carry their own session identifiers, so it is
// treated as a credential belonging to someone else.
//
// The zero ClientState means the client sent no state, which is legal.
type ClientState struct {
	m *stateMaterial
}

// ParseClientState validates a presented state parameter. An empty state is
// absent, not invalid. A state that is over-long, or that carries a byte which
// could split a header or a log line, is refused.
func ParseClientState(raw string) (ClientState, error) {
	if raw == "" {
		return ClientState{}, nil
	}
	if len(raw) > MaxClientStateLen {
		return ClientState{}, fmt.Errorf("state parameter is %d bytes, the limit is %d: %w",
			len(raw), MaxClientStateLen, ErrInvalidState)
	}
	for i := range len(raw) {
		if raw[i] < 0x20 || raw[i] == 0x7F {
			return ClientState{}, fmt.Errorf(
				"state parameter carries a control byte at offset %d: %w", i, ErrInvalidState)
		}
	}
	held := stateString(raw)
	return ClientState{m: &stateMaterial{value: &held}}, nil
}

// Reveal returns the state exactly as the client sent it. Its only legitimate
// caller is the code that builds the authorization response.
func (s ClientState) Reveal() string {
	if s.m == nil || s.m.value == nil {
		return ""
	}
	return string(*s.m.value)
}

// IsZero reports whether the client sent no state.
func (s ClientState) IsZero() bool { return s.Reveal() == "" }

type redactedState struct {
	Type    string `json:"type"`
	Present bool   `json:"present"`
	Length  int    `json:"length"`
}

func (s ClientState) redacted() redactedState {
	return redactedState{
		Type:    "oauthserver.ClientState",
		Present: !s.IsZero(),
		Length:  len(s.Reveal()),
	}
}

// String reports the presence and length of the state, never its bytes.
func (s ClientState) String() string {
	red := s.redacted()
	return "oauthserver.ClientState{value:" + presence(red.Present) +
		" length:" + strconv.Itoa(red.Length) + "}"
}

// GoString satisfies the %#v verb with the same redacted rendering.
func (s ClientState) GoString() string { return s.String() }

// MarshalJSON serializes the redacted form.
func (s ClientState) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.redacted())
}

// LogValue implements slog.LogValuer.
func (s ClientState) LogValue() slog.Value {
	red := s.redacted()
	return slog.GroupValue(
		slog.String("type", red.Type),
		slog.Bool("present", red.Present),
		slog.Int("length", red.Length),
	)
}

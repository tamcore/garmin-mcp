package auth

import (
	"encoding/json"
	"log/slog"
)

// Credentials carries one Garmin account email and password for the duration of
// a single login call.
//
// It is a request-scoped value and nothing in this package retains one: no
// Machine, no registry entry and no TokenSet holds a Credentials, so a password
// never outlives the request that supplied it.
//
// Like TokenSet it is secret-bearing and hides its material behind a pointer to
// unexported fields, so a reflective logger, a direct field print and a
// method-stripping alias cannot read it.
type Credentials struct {
	// secrets is a pointer on purpose; see TokenSet.secrets.
	secrets *credentialSecrets
}

// credentialSecrets is the sealed content of a Credentials. Both halves sit behind
// a pointer, so no rendering path can reflect them out; see secretString.
type credentialSecrets struct {
	email    *secretString
	password *secretString
}

// NewCredentials seals an email and password for one login call.
func NewCredentials(email, password string) Credentials {
	return Credentials{secrets: &credentialSecrets{
		email:    sealSecret(email),
		password: sealSecret(password),
	}}
}

func (c Credentials) s() credentialSecrets {
	if c.secrets == nil {
		return credentialSecrets{}
	}
	return *c.secrets
}

// Email is the account identifier. It is personal data: it belongs in a request
// body, never in a log line.
func (c Credentials) Email() string { return revealSecret(c.s().email) }

// Password is the account password. It belongs in a request body and nowhere
// else: never a field, a log, an error or a store.
func (c Credentials) Password() string { return revealSecret(c.s().password) }

// IsZero reports whether either half is missing.
func (c Credentials) IsZero() bool {
	secrets := c.s()
	return secrets.email == nil || secrets.password == nil
}

// redactedCredentials is the only shape a Credentials is ever rendered in.
type redactedCredentials struct {
	Type        string `json:"type"`
	HasEmail    bool   `json:"emailPresent"`
	HasPassword bool   `json:"passwordPresent"`
}

func (c Credentials) redacted() redactedCredentials {
	secrets := c.s()
	return redactedCredentials{
		Type:        "auth.Credentials",
		HasEmail:    secrets.email != nil,
		HasPassword: secrets.password != nil,
	}
}

// String renders presence only.
func (c Credentials) String() string {
	red := c.redacted()
	return "auth.Credentials{email:" + presence(red.HasEmail) +
		" password:" + presence(red.HasPassword) + "}"
}

// GoString satisfies the %#v verb with the same redacted rendering.
func (c Credentials) GoString() string { return c.String() }

// MarshalJSON serializes the redacted form.
func (c Credentials) MarshalJSON() ([]byte, error) { return json.Marshal(c.redacted()) }

// LogValue implements slog.LogValuer.
func (c Credentials) LogValue() slog.Value {
	red := c.redacted()
	return slog.GroupValue(
		slog.String("type", red.Type),
		slog.Bool("emailPresent", red.HasEmail),
		slog.Bool("passwordPresent", red.HasPassword),
	)
}

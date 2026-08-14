// Package oauthserver is the MCP-facing OAuth component of this server: the
// protected-resource side that verifies bearer tokens, and, in the self-contained
// deployment mode, the authorization-server side that issues them.
//
// The package deliberately owns only the OAuth protocol boundary and the
// transaction state that connects an authorization request to a completed Garmin
// login. It renders no browser page — internal/loginweb owns those — and it never
// touches Garmin. The two credential boundaries stay separate: a token issued
// here is never forwarded to Garmin, and a Garmin DI token set is never returned
// to an MCP client.
//
// # Shape of the API
//
// Every value that carries security meaning is a validated type with unexported
// fields, so it can only be constructed through a function that checks it:
// [Secret], [Lookup], [ScopeSet], [RedirectURI], [Resource], [CodeChallenge] and
// [Client]. A handler that holds one of these has already passed the check the
// type name implies.
//
// [Server] is the protocol surface. It is safe for concurrent use, holds no
// mutable package state, and reaches persistence only through the consumer-side
// interfaces in store.go, so the storage implementation is replaceable and the
// tests use fakes.
//
// # Non-negotiables
//
// These are properties of the code, enforced by tests, not aspirations:
//
//   - Authorization code with PKCE S256 is the only grant that issues a token
//     from a user interaction. There is no implicit grant, no resource-owner
//     password grant, and a downgrade to "plain" is refused.
//   - The issuer, the registered redirect URI and the resource indicator match
//     exactly. An authorization error is never redirected to a missing,
//     malformed or unregistered redirect URI.
//   - The client's opaque state is returned byte for byte and is never used as
//     this server's own CSRF or session state.
//   - Codes and both token types carry at least 256 bits of entropy from
//     crypto/rand, and only a SHA-256 lookup value is ever persisted.
//   - No token, code, verifier or state reaches an error, a log record or a
//     String output.
package oauthserver

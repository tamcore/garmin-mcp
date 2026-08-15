# ADR 0003 — OAuth authorization-server library

## Status

Accepted, 2026-08-14. The authorization-server role is implemented in
`internal/oauthserver` on the standard library and the pinned SDK's types. No
third-party OAuth-server framework is taken.

## Context

The server acts as an OAuth protected resource, and in the self-contained
deployment mode also as an authorization server. The required surface includes
authorization code with PKCE S256, RFC 9728 Protected Resource Metadata, the
RFC 6750 challenge, RFC 8414 authorization-server metadata, RFC 8707 resource
indicators, exact issuer and redirect matching, per-client consent, rotating
refresh tokens with family revocation, and transactional revocation cascades.

Building this from ad-hoc handlers is prohibited. A maintained library such as
`ory/fosite` was the intended path where the SDK does not cover the role.

### What the pinned SDK actually provides

Confirmed by reading `modelcontextprotocol/go-sdk` v1.7.0 in the module cache:

- `auth.RequireBearerToken`, `auth.TokenVerifier`, `auth.TokenInfo`,
  `auth.ProtectedResourceMetadataHandler`;
- `oauthex.ProtectedResourceMetadata`, `oauthex.AuthServerMeta`,
  `oauthex.MatchesResource`, `oauthex.Challenge`, `oauthex.ParseWWWAuthenticate`,
  and client-side dynamic-registration types.

The SDK's only authorization server is `internal/oauthtest`, which is a test fake
and cannot be imported. The resource-server role is therefore taken from the SDK;
the authorization-server role has no SDK implementation to take.

Two SDK limitations are worked around in local code rather than by patching the
SDK, which the brief forbids:

1. `auth.RequireBearerToken` emits a challenge carrying only `resource_metadata`
   and `scope`, and never an `error` code, so it cannot distinguish a missing
   token from an invalid one. `internal/oauthserver` provides its own handler and
   still exposes `Server.TokenVerifier()` for callers that prefer the SDK path.
2. `oauthex.AuthServerMeta.JWKSURI` has no `omitempty`, so serving that struct
   directly publishes `"jwks_uri":""`. The metadata is marshalled through a local
   wire type that omits it, asserted by test.

## Decision

Implement the authorization-server role locally, behind the `Store` interface set
in `internal/oauthserver`.

`ory/fosite` was evaluated and rejected on four grounds:

- **Fit.** Its value is concentrated in OIDC, JWT issuance, introspection and a
  wide grant matrix. This server issues opaque 256-bit tokens verified by a hashed
  lookup, which is the part fosite adds least to. The project would use a small
  fraction of it and inherit all of its API and vulnerability surface.
- **Storage cost.** fosite requires roughly ten storage interfaces implemented on
  its terms. The SQLite store is built to this project's schema, and the two
  properties the design rests on — atomic code consumption, and reuse detection
  that revokes the family in the same transaction — would have to be
  re-established inside fosite's contract anyway.
- **The security-critical policy stays local either way.** Exact redirect policy,
  the decision to render locally rather than redirect an error, byte-exact client
  state, consent bound to the full tuple, and RFC 8707 exact-audience validation
  are either absent from fosite or not in the shape the brief requires.
- **Stability.** fosite is Apache-2.0 and maintained, but pre-1.0 with breaking
  changes across minor versions. Pinning a pre-1.0 API for a boundary that must be
  audited line by line is a poor trade, and it would add a transitive tree for
  code that mostly would not run.

This is not the prohibited "ad-hoc handlers" outcome. It is one audited component
behind local interfaces, with an explicit negative-test matrix, and no other
package depends on its internals.

### Consent and client registration

Consent is keyed on `(principal, client id, exact redirect URI, resource)`, and
the consented scopes are the **value** rather than part of the key. A request is
admitted only when `Consent.Covers` shows the requested set is a subset of the
consented set. That gives the property the brief asks for — a scope increase or a
redirect change requires fresh consent — while a narrower request reuses the
existing record instead of prompting again. Tested directly, and the schema in
`migrations/0002_oauth_contract.sql` carries the same four-column primary key.

Client registration is preregistration only. The authorization-server metadata
advertises no `registration_endpoint`, asserted by test. Dynamic registration and
Client ID Metadata Documents stay deferred in `docs/mcp-version-matrix.md`. No
vendor client ID is hardcoded.

PKCE has no operator switch. No configuration accepts `plain` or disables the
check, because a flag that can weaken a mandatory control is a foot-gun. "Public
clients only when PKCE is enforced" is structural here, not conditional.

## Consequences

- The project owns conformance to RFC 6749, 6750, 7009, 7636, 8414, 8707 and 9728
  and to MCP 2026-07-28 authorization, including future spec drift.
- Deliberately not implemented: OIDC, JWT and JWKS, introspection (RFC 7662),
  dynamic registration (RFC 7591), Client ID Metadata Documents, device and CIBA
  grants, token exchange, DPoP, and mTLS-bound tokens.
- Redirect matching is exact, including the port, so a native client using an
  ephemeral loopback port cannot be preregistered. If the client interoperability
  matrix requires it, add an explicit per-client opt-in for RFC 8252 §7.3 rather
  than relaxing matching globally.
- `http://localhost` is refused; the literal `127.0.0.1` and `[::1]` are accepted,
  per RFC 8252 §8.3.
- The choice is replaceable: `Store` is the only seam to re-point.
- The OAuth negative matrix and the named-client interoperability tests are the
  **only** verification of this component. They were always separate from the MCP
  conformance suite, which does not certify an embedded authorization server; and
  that suite turned out to be unable to score this server at all, so no external
  signal exists. See ADR 0002.

# ADR 0003 — OAuth authorization-server library

## Status

Open. Decided in phase 4, before any authorization endpoint is implemented.

## Context

The server acts as an OAuth protected resource, and in the self-contained
deployment mode also as an authorization server. The required surface includes
authorization code with PKCE S256, RFC 9728 Protected Resource Metadata, the
RFC 6750 challenge, RFC 8414 authorization-server metadata, RFC 8707 resource
indicators, exact issuer and redirect matching, per-client consent, rotating
refresh tokens with family revocation, and transactional revocation cascades.

Building this from ad-hoc handlers is prohibited. The official Go SDK covers the
resource-server role through its `auth` and `oauthex` packages; the actual
capability at the pinned stable release must be confirmed before the decision.
Where the SDK does not implement the authorization-server role, a well-maintained
Go OAuth server library such as `ory/fosite` is the intended path.

## Decision

Confirm what the pinned official SDK provides for the resource-server role and use
it there. For the authorization-server role, select either the SDK (if it
suffices) or a maintained third-party library, and wrap the choice behind local
interfaces in `internal/oauthserver` so no other package depends on it.

Completing this ADR requires:

- the confirmed capability of the pinned SDK `auth` and `oauthex` packages;
- the selected authorization-server library with version, license, and
  maintenance status;
- the local interface boundary that isolates it;
- the client-registration mode chosen from preregistration, constrained RFC 7591
  registration, or Client ID Metadata Documents, with the interoperability matrix
  for the intended MCP clients;
- confirmation that private SDK behavior is not patched.

## Consequences

- The library choice is replaceable, because it sits behind local interfaces.
- The OAuth negative matrix and the named-client interoperability tests stay
  separate from the MCP conformance suite. The conformance suite does not certify
  the embedded authorization server.
- No vendor client ID is hardcoded, whatever the library.

# ADR 0009 — redirect wildcards and the login allowlist

## Status

Accepted, 2026-09-08.

## Context

A hosted MCP client — ChatGPT and similar — can carry a per-installation or
per-conversation redirect path that this server's operator cannot know in
advance. Byte-exact redirect matching, which ADR 0002's OAuth model otherwise
treats as absolute, forces a redeploy of `oauth-clients` for every new user of
such a client. Dynamic client registration remains refused (ADR 0003), so the
registered redirect URI is the only lever left for serving more than one user
of a hosted client without an operator redeploy on every new user.

## Decision

Two operator-facing settings, both default off, so a deployment that sets
neither is unchanged:

**`oauth-allow-redirect-wildcards`** admits exactly one wildcard shape in a
client's `redirect-uris`: a trailing-path wildcard, where `*` is the final
byte, appears exactly once, and is immediately preceded by `/`. The prefix
before it must pass every rule an exact registration passes — absolute, a
host, `https` (or `http` only for a loopback host), lower-case
scheme, no userinfo, no fragment, no control byte, within the URI length bound
— and carry no query. A presented candidate matches only when the remainder
after the prefix is non-empty and is built entirely from
`A-Za-z0-9-._~/` with no `.` segment, no `..` segment, and no empty segment.
`Client.MatchRedirectURI` still returns the concrete presented URI, never the
pattern, so the authorization code, the consent row, and the issued token all
bind to an exact redirect.

**`login-allowed-emails`** restricts which Garmin account addresses may
complete the remote browser login. It is checked in the remote credential
handler, before the Garmin login call, so a refused address costs the
deployment zero requests to Garmin; a refused attempt renders the same generic
message a Garmin-rejected credential renders and still consumes an attempt from
the transaction budget, so the rendered response is not an enumeration oracle
nor a free probe loop. It is not identical in timing: the allowlist path
returns without a network round trip, while a wrong password pays a full HTTPS
request to Garmin, so an unauthenticated caller can time the difference. That
gap is the direct, unavoidable and accepted cost of never forwarding an
unauthorized address upstream. The setting has no command-line flag,
deliberately — every other layer treats an address as unprintable, and a flag
would publish the whole allowlist onto the process command line.

## Why not a host wildcard

A host wildcard (`https://*.example.com/callback`, or admitting `*` anywhere
but a trailing path segment) moves the trust boundary from this server's own
registration to DNS. Any subdomain takeover, or any client the operator did not
anticipate standing up under that domain, becomes authorization-code theft
without this server doing anything wrong. A trailing-path wildcard keeps the
scheme and the host exact — the boundary this server controls by registering
the pattern stays the boundary that decides — and narrows the residual risk to
paths under one prefix on one host the operator chose.

## Why the traversal normalization is load-bearing

A prefix comparison alone is not enough. `https://host/cb/../../evil` begins
with the literal bytes of the prefix `https://host/cb/`, so a naive
`strings.HasPrefix` admits it — and a browser resolves `..` segments before it
ever sends the request, landing the redirect at `https://host/evil`, entirely
outside the intended prefix. Without refusing `.` and `..` segments (and an
empty segment, which a mid-path `//` produces) in the matched remainder, a
path wildcard *is* a host wildcard: every path on the host becomes reachable
through traversal, regardless of how narrow the registered prefix looks.
`isSafePathRemainder` closes this with an allowlist of safe bytes rather than a
blocklist of `.`/`..`, because a blocklist misses percent-encoded traversal
(`%2e%2e`) and backslash-as-separator, both of which a WHATWG-conformant
browser treats identically to the literal forms.

## Why consent stays exact

`MatchRedirectURI` resolves a presented URI against the client's exact URIs
first, then its patterns, and on a pattern match returns the concrete
presented value — never a value that carries the pattern's own `*`. Every
downstream binding (authorization code, consent row, issued token) is keyed on
that concrete redirect. A new concrete redirect a pattern has never seen before
therefore still requires the user's own fresh consent: one grant under a
wildcarded client never becomes a standing grant over every path the pattern
could ever admit.

## Why the allowlist is not a compensating control

The two settings are independent, and the allowlist compensates for nothing
about the wildcard. The redirect-wildcard attack works by getting a legitimate,
allowlisted user to complete their own login and issue their own authorization
code under crafted parameters that redirect that code somewhere the attacker
controls, inside the wildcarded prefix. `login-allowed-emails` never enters
that path: the victim is exactly the kind of user the allowlist is designed to
let through. Treating the allowlist as a mitigant for the wildcard would be a
false sense of security; each setting is documented and evaluated on its own
risk.

## Consequences

- A deployment that sets neither setting behaves exactly as before this
  change: exact redirect matching only, and every Garmin account may attempt
  the remote login.
- Enabling `oauth-allow-redirect-wildcards` is a deliberate, logged trade
  (`internal/cmd/clientstore.go`'s `warnWildcardRedirects` warns once per
  registered pattern at every start-up) between operator convenience and a
  residual authorization-code-theft risk scoped to the registered prefix. The
  residual risk, including the whole-host case `https://host/*`, is recorded in
  `docs/threat-model.md`. This is a genuine capability regression from the
  exact-matching baseline ADR 0002 established, not a neutral convenience
  feature, and every downstream document that describes it is required to say
  so plainly rather than soften it into a feature description.
- `login-allowed-emails` gates login, where a principal is created. It is not a
  kill switch: removing an address does not terminate a principal that already
  exists. That principal's OAuth authorization is revoked through
  `garmin-mcp revoke --principal` and its Garmin account link through
  `garmin-mcp unlink --principal`, the same commands every other principal
  revocation uses — this feature adds no new revocation path.
- Validation of both settings is checked twice, in `internal/config` (the
  coarse shape gate) and in `internal/oauthserver` / `internal/loginweb` (the
  authoritative fine-grained rules), because `internal/config` must not import
  a server package. A registration that clears the coarse gate can still fail
  the fine one at start-up; that is fail-closed, working as intended, and it is
  documented in `docs/configuration.md` so a puzzling start-up error is
  recognizable.
- Resource indicators (`resources`) never admit a wildcard shape, with or
  without `oauth-allow-redirect-wildcards`: the shape check they share with
  redirect URIs is called with the wildcard flag hard-coded false on that path.

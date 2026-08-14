# ADR 0001 — Native Garmin login feasibility and login transport

## Status

Accepted. The phase-0 feasibility gate is **closed** with outcome **GO**, run by
the maintainer on 2026-08-14. Do not re-run a live credential login to
re-establish this decision, and do not ask for credentials.

## Context

The whole project depends on one unproven assumption: that a native Go HTTP
client can complete a Garmin Connect login. The pinned reference,
`python-garminconnect` 0.3.8, reaches Garmin through `curl_cffi`, which applies
browser TLS impersonation, and it paces the GET to POST sequence by a randomized
10 to 20 seconds. Standard Go TLS cannot reproduce a `curl_cffi` fingerprint.
Everything in the later auth, remote-OAuth, and tool-breadth phases is worthless
if plain `net/http` cannot log in.

The gate scope was minimal: the `LoginTransport` interface, a plain `net/http`
implementation, the mobile-iOS, widget, and portal request shapes from the pinned
source, the failure classifier, and an opt-in live check behind the `garminlive`
build tag.

### Evidence

Reproduced from a throwaway `net/http` probe against `sso.garmin.com` from a
single residential/office IP. No credentials, cookies, tokens, or raw bodies are
recorded here.

Credential-free stage:

| Request | Result |
|---------|--------|
| `GET /sso/embed` | 200 |
| `GET /sso/signin` | 200, `_csrf` present |
| `POST /mobile/api/login`, empty body | 400 `application/problem+json` |
| `POST /portal/api/login`, empty body | 400 `application/problem+json` |

Every response carried `Server: cloudflare` with an empty `cf-mitigated` header.
No challenge, no 403, and no 429.

Credential stage:

- `POST /mobile/api/login?clientId=GCM_IOS_DARK&locale=en-US&service=https://mobile.integration.garmin.com/gcm/ios`
  with the iOS user agent returned **200** with
  `responseStatus.type = "SUCCESSFUL"` and a service ticket.
- Confirmed on **two separate accounts**, with no TLS impersonation and no pacing
  delay before the POST.

### What the evidence does not settle

- The evidence comes from **one source IP**. Datacenter and CI egress may be
  scored differently by Cloudflare, so datacenter viability is unproven.
- Neither test account had **MFA enabled**, so the live `MFA_REQUIRED`
  continuation is unproven and must be covered by fake-service tests.
- Upstream's impersonation profiles and randomized 10 to 20 second GET to POST
  pacing exist because this surface has blocked clients before. Drift is
  expected.

## Decision

Implement `net/http` only as the login transport, behind the injectable
`LoginTransport` interface. Keep `CGO_ENABLED=0`.

Do not add utls, curl-impersonate, cgo, Python, or browser automation. Never
subprocess a `curl-impersonate` binary, never ship an unpinned prebuilt blob, and
never attempt to bypass CAPTCHA or WAF controls.

Because of the unsettled parts:

- Keep the GET to POST pacing behavior configurable.
- Keep the failure classifier exhaustive: success, invalid credentials, MFA
  required, CAPTCHA/WAF/forbidden, rate limited, and temporary transport error.
  Stop the strategy fallback on definitive invalid credentials.
- Keep this gate re-runnable as an opt-in `garminlive` command and test for drift
  detection, with an explicit environment acknowledgement and a dedicated
  non-primary account. It never runs in ordinary CI.

The fingerprint-transport ladder stays documented contingency, not planned work.
If the opt-in live check later fails while the same request shape succeeds from a
browser-fingerprinted client, re-enter this decision and work down the ladder,
stopping at the first rung that demonstrably logs in:

1. pure-Go TLS fingerprinting (a `refraction-networking/utls`-class library),
   which keeps `CGO_ENABLED=0`, the static distroless image, and
   cross-compilation intact;
2. a cgo binding to `lexiforest/curl-impersonate`, which requires an optional
   build tag so the default release binary stays pure Go, a separate non-static
   image variant, the library built from source at a pinned upstream release with
   verified checksums, and per-platform CI coverage for every published artifact.

Any such choice needs its evidence, the reason for skipping earlier rungs, and a
dependency and security review recorded here as an amendment.

## Consequences

- The default and only shipped transport is `net/http`. Release binaries stay
  `CGO_ENABLED=0` and the runtime image stays distroless static.
- No transport ladder work is scheduled. The ladder is contingency only.
- The `LoginTransport` interface must keep any future transport out of every
  other package, and every transport must pass the same fake-service test suite.
- MFA continuation correctness rests entirely on fake-service tests until a live
  MFA-enabled account is available.
- CI and datacenter egress behavior is an accepted open risk. A failure there
  shows up as a classified CAPTCHA/WAF outcome, not as a silent bad-password
  error.
- Phase 0 no longer blocks any milestone. The gate stays re-runnable, and a
  future live failure reopens this ADR.

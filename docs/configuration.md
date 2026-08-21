# Configuration

This document lists every setting `garmin-mcp` reads, where it can be set, what
it defaults to, what rejects it, and which transport it applies to.

Garmin credentials are not configuration. There is no password setting, no MFA
setting, and no account selector, on any layer. Credentials are entered only in
the browser login form or at the terminal prompt of `garmin-mcp auth`.

## One name, three spellings

Each setting has one canonical key. The key is the flag name and the
configuration-file key. The environment variable is the key upper-cased, with
each hyphen replaced by an underscore, behind the prefix `GARMIN_MCP_`.

```
key:  session-timeout
flag: --session-timeout
env:  GARMIN_MCP_SESSION_TIMEOUT
file: session-timeout: 30m
```

## Precedence

`internal/config.Load` resolves each setting in this order. The first layer that
supplies a value wins:

1. a command-line flag the operator actually changed;
2. the `GARMIN_MCP_` environment variable;
3. the configuration file;
4. the built-in default.

A flag that was not given on the command line does not take part, even though it
has a default. That is why a default never masks an environment variable or a
file value.

The configuration file itself is selected by the same order: `--config`, then
`GARMIN_MCP_CONFIG`. There is no implicit search path, so an unset value reads no
file. The file's extension selects the parser (for example `.yaml`, `.yml`,
`.json`, `.toml`). All examples in this repository use YAML.

## Restart

No setting is re-read while the process runs. There is no configuration watch and
no reload signal. Every change needs a restart. A "restart required" column is
therefore not repeated per setting: it applies to all of them.

## Validation

Validation runs completely, before anything is opened. No listener binds, no key
file is read, and no database is created for a configuration that is going to be
rejected. Every problem found is reported at once, joined into one error, so an
operator does not rediscover the next fault after each restart.

Validation is lexical only. It checks the shape of a path but does not open it;
ownership and mode are enforced by the package that opens the file, at open time.

Configuration validation is not the whole gate. Some rules can only be applied
when the deployment is assembled — see [Start-up refusals](#start-up-refusals).

## Settings

`stdio` and `remote` name the two transports. `remote` is
`transport: streamable-http`.

### Transport and process

| Key | Flag | Default | Applies to | Validation |
|-----|------|---------|-----------|-----------|
| `config` | `--config` | none | both | Path to a configuration file. An unreadable or unparsable file is a start-up failure. |
| `transport` | `--transport` | `stdio` | both | `stdio` or `streamable-http`. Trimmed and case-folded; empty selects `stdio`. Anything else is rejected without echoing the value. |
| `region` | `--region` | `garmin.com` | both | `garmin.com` or `garmin.cn`. Empty selects `garmin.com`. A China account under the global region cannot log in, and the region is never guessed from a credential. |
| `principal-id` | `--principal-id` | `local` | stdio | Not blank, no leading or trailing space, at most 256 bytes, no `@`, no control characters. It is an opaque storage key, never an email and never a Garmin account selector. Remotely the principal comes from the verified bearer token, and this setting is ignored. |
| `state-dir` | `--state-dir` | per-user configuration directory + `/garmin-mcp` | both | No `..` segment. Holds `keys/` and `tokens/`. On a Kubernetes volume mount, point this at a subdirectory of the mount, never at the mount root — see [operations.md](operations.md#the-state-directory-on-a-kubernetes-volume-mount). |
| `log-level` | `--log-level` | `info` | both | `debug`, `info`, `warn`, or `error`. An unknown level is rejected rather than defaulted. |
| `log-format` | `--log-format` | `text` | both | `text` or `json`. |

Logs never go to standard output. In stdio mode that stream carries MCP frames
only.

### Network (remote)

| Key | Flag | Default | Applies to | Validation |
|-----|------|---------|-----------|-----------|
| `bind-address` | `--bind-address` | `127.0.0.1:8180` | remote | `host:port`, port 1–65535. A non-loopback bind is refused unless at least one of these is set: a TLS certificate, one or more trusted proxy networks, or `allow-insecure-http`. It carries a default, so stdio mode does not reject it; it is simply unused there. |
| `public-url` | `--public-url` | none | remote | Required. Absolute `http` or `https` URL with a host, no userinfo, no query, no fragment. A cleartext non-loopback origin needs `allow-insecure-http`. **Configuration validation is not the last word: the authorization server refuses any issuer that is not `https`, so a plain-http public URL passes here and then fails at start-up.** Rejected outright in stdio mode. |
| `trusted-proxy-cidrs` | `--trusted-proxy-cidrs` | empty | remote | CIDR networks, for example `10.0.0.0/8`. A bare address is rejected. Empty trusts no forwarded header. Rejected outright in stdio mode. |
| `allowed-origins` | `--allowed-origins` | empty | remote | Bare absolute `http` or `https` origins. A path, query, fragment, or userinfo is rejected, because such an entry can never match a browser `Origin` header. Empty denies every request that carries an `Origin`. Rejected outright in stdio mode. |
| `allow-insecure-http` | `--allow-insecure-http` | `false` | remote | Development override. It permits a cleartext non-loopback bind and origin at the configuration layer. It does not make a cleartext issuer acceptable. Rejected outright in stdio mode. |
| `tls-cert-file` | `--tls-cert-file` | none | remote | PEM certificate chain. Requires `tls-key-file`. No `..` segment. Rejected outright in stdio mode. |
| `tls-key-file` | `--tls-key-file` | none | remote | PEM private key. Requires `tls-cert-file`. No `..` segment. Rejected outright in stdio mode. |
| `session-timeout` | `--session-timeout` | `30m` | remote | Between 30s and 24h. It bounds how long an idle Streamable HTTP session stays addressable. Ignored in stdio mode, and not bounds-checked there. |

List settings accept a YAML sequence, a repeated flag, or a comma-separated
environment variable. Empty entries are dropped.

### State and secret material

| Key | Flag | Default | Applies to | Validation |
|-----|------|---------|-----------|-----------|
| `database-path` | `--database-path` | none | remote | Required for remote. No `..` segment. Accepted but unused in stdio mode, which stores its single account in files under `state-dir`. |
| `master-key-file` | `--master-key-file` | none | both | Required for remote. **The value selects the directory the versioned key material lives in, not the file name.** The file name is always `key-v<version>.json` and is owned by `internal/cryptostore`. No `..` segment. |
| `master-key` | none | none | neither in practice | Inline key material. It has no flag on purpose: a command line is readable by every local process. Configuration validation refuses it for remote and accepts it for stdio, but the stdio composition root refuses it too, so an inline master key fails to start in both modes. Use `master-key-file`. |
| `garmin-tokens-file` | `--garmin-tokens-file` | none | stdio | A mounted native Garmin DI token file to import on the first stdio start. No `..` segment. Accepted but unused in remote mode, where each account is linked through the browser flow. |
| `garmin-tokens` | none | none | stdio | An inline native Garmin DI token document. No flag, for the same reason as `master-key`. Refused in remote mode. It is an explicitly insecure compatibility override; the mounted file is the supported form. |

Setting an inline secret and its `-file` companion together is rejected. Guessing
which one was meant could put a stale key into production.

Both `-file` settings are validated for shape here but not opened here. Reading
them belongs to the package that owns them, which enforces ownership and mode at
the same moment it reads.

### The OAuth client registry (remote)

| Key | Flag | Default | Applies to | Validation |
|-----|------|---------|-----------|-----------|
| `oauth-clients` | none | empty | remote | At least one entry is required for remote, at most 32. Rejected outright in stdio mode. |

The registry is a list of records, so it has no flag: a command line cannot spell
one record. Two spellings are accepted, and both parse to the same shape:

- a configuration-file list, which is the normal form;
- a JSON array in `GARMIN_MCP_OAUTH_CLIENTS`, for a container deployment that
  mounts no file.

There is no dynamic client registration, and no vendor client is ever defaulted.
A client exists because an operator wrote it here.

Each entry:

| Sub-key | Required | Validation |
|---------|----------|-----------|
| `id` | yes | Unique in the registry, at most 256 bytes, no surrounding space, no control characters. This is the key reconciliation and every lookup use. |
| `name` | no | At most 128 bytes, no control characters. Shown on the disclosure page. An empty name makes the page show the identifier. |
| `redirect-uris` | yes | 1 to 8 exact URIs, at most 2048 bytes each. Absolute, with a host, no userinfo, no fragment, no `*`, no space and no control character. `https` always; `http` only for a **literal** loopback address. The name `localhost` is not accepted, because it resolves through a resolver an attacker may influence. The host must not be an IPv6 literal (`http://[::1]:port/...` or `https://[2001:db8::1]/...`): CSP3's `host-source` grammar has no production for a bracketed IPv6 literal, so a CSP-enforcing browser blocks the consent page's redirect to that client regardless of scheme; register a hostname, or an IPv4 loopback address such as `127.0.0.1`, instead. Matching at authorization time is byte-exact. |
| `scopes` | yes | At least one non-blank scope, at most 32, each at most 128 bytes. This is the widest set the client may ever be granted, and the deployment advertises the union of every client's scopes. Two names are meaningful to the tool policy: `garmin:write` gates the write tier and `garmin:destructive` gates the destructive tier, and neither implies the other. The read tier is not scope-gated, so a read-only client still needs a scope but the name is the operator's own; `garmin:read` is the convention this repository uses. |
| `resources` | yes | 1 to 8 RFC 8707 resource indicators, same URI rules as a redirect URI. This is the audience a token is minted for. |
| `public` | no | `true` selects token endpoint authentication method `none`, which is safe only because PKCE S256 is mandatory. A public client must carry no secret digest. |
| `secret-hash-file` | for a confidential client (alternative to `secret-hash`) | A file holding the hex SHA-256 of the client secret. At most 128 bytes are read. The file must be a regular file — a symlink is refused — and owner-only, so a projected Kubernetes Secret volume cannot satisfy this setting: its keys are symlinks into a shared, non-owner-only-readable data directory. Prefer this form wherever a plain owner-only regular file is possible. |
| `secret-hash` | for a confidential client (alternative to `secret-hash-file`) | The hex SHA-256 digest of the client secret, inline. Exactly one of `secret-hash` or `secret-hash-file` must be set for a confidential client, never both. This is the form to use in a container deployment where the digest arrives through an environment variable rather than a mountable file. |

Computing the digest and using it safely:

```sh
# Linux
printf '%s' 'the-client-secret' | sha256sum | cut -d' ' -f1
# macOS, which ships shasum rather than sha256sum
printf '%s' 'the-client-secret' | shasum -a 256 | cut -d' ' -f1
```

Either field takes the **digest**, never the secret itself — pasting the raw
client secret into `secret-hash` will not authenticate. The digest is unsalted,
single-round SHA-256, so it is password-verifier material, not a replayable
credential: a low-entropy client secret is recoverable offline from a leaked
digest. Generate the client secret with a cryptographically strong random
source and enough length that this offline search is infeasible.

Example:

```yaml
oauth-clients:
  - id: example-desktop-client
    name: Example Desktop Client
    redirect-uris:
      - http://127.0.0.1:33418/callback
    scopes:
      - garmin:read
    resources:
      - https://mcp.example.invalid/mcp
    public: true
```

### Tool policy

| Key | Flag | Default | Applies to | Validation |
|-----|------|---------|-----------|-----------|
| `enable-write-tools` | `--enable-write-tools` | `false` | both | Sufficient authorization on local stdio. Remotely, a granted OAuth write scope is also required. |
| `enable-destructive-tools` | `--enable-destructive-tools` | `false` | both | Requires `enable-write-tools`. Sufficient tier authorization on local stdio; remote callers also need `garmin:destructive`. Every destructive call still needs client confirmation. |
| `tool-allowlist` | `--tool-allowlist` | empty | both | Lower-case tool names of letters, digits, and underscores, at most 64 bytes, no repeats. A non-empty list restricts registration to those names. It can only narrow local operator authority or remote scope authorization. A name that this build does not register is a start-up failure, so a typo cannot silently allow nothing. |
| `tool-denylist` | `--tool-denylist` | empty | both | Same name rules. A name may not appear in both lists. |

### Limits

| Key | Flag | Default | Applies to | Validation |
|-----|------|---------|-----------|-----------|
| `max-request-bytes` | `--max-request-bytes` | `1048576` (1 MiB) | remote | 1 to 8388608 (8 MiB). It bounds a decoded MCP request body. The browser login forms use their own fixed 8 KiB bound. |
| `max-response-bytes` | `--max-response-bytes` | `8388608` (8 MiB) | none today | 1 to 67108864 (64 MiB). It is validated but not yet passed to the Garmin request layer, which applies its own default of 8 MiB. Changing this setting has no effect. |
| `request-timeout` | `--request-timeout` | `30s` | both | Between 1s and 10m. It bounds one outbound Garmin call. |
| `read-rate-limit` | `--read-rate-limit` | `120` | both | 1 to 100000 read tool calls per principal per minute. |
| `write-rate-limit` | `--write-rate-limit` | `30` | both | 1 to 100000 write tool calls per principal per minute. |
| `safety-delay` | `--safety-delay` | `0` (disabled) | both | 0 to 5m. It pauses each write and destructive tool call after every gate has allowed it and before it runs. Zero disables the pause. See below. |

#### The safety delay

`safety-delay` inserts a pause before a write or destructive tool reaches Garmin.
It is `0` by default: an upgrade changes no deployment's timing, and a delay is a
choice an operator makes rather than a cost every write pays.

What it does, precisely:

- It applies to the **write and destructive tiers only**. A read changes nothing,
  so there is nothing to reconsider during the pause and it would be latency with
  no safety in it.
- It runs **after every gate** — tier and scope, the rate limiter, and destructive
  confirmation. A refused call never waits. Waiting to say no would cost the server
  the wait and would teach a prober how long the gate takes.
- The wait is **interruptible**, and that is the point rather than a detail. A
  caller that cancels during the pause stops the call: the tool never runs and
  nothing is sent to Garmin. A pause nothing can interrupt is latency, not safety.
- It applies **once per tool call**, not once per item, so a batch tool waits once
  however many objects it carries.

What it does not do. It is not a second confirmation: destructive tools already
require elicitation that fails closed, and this pause neither replaces nor
strengthens that. Its value is on the **write** tier, which has no interactive gate
at all — that is where the cancellation window is the only one a caller gets.

The ceiling is 5 minutes. A longer pause outlives the patience of every MCP client,
so the call would be abandoned rather than delayed, which is not what the setting is
for.

Every limit has a ceiling of its own, so an operator cannot disable a protection
by raising it without bound. The burst allowance follows the rate: lowering a
rate below the shipped burst lowers the burst with it.

## Start-up refusals

These rules run after configuration validation passes. They are start-up
failures, not configuration errors, and an operator meets them on the first
`serve` run:

- **A cleartext issuer.** The public URL must be `https`. A loopback `http`
  public URL passes configuration validation and then fails here.
- **A public URL path this deployment serves itself**, that is
  `/.well-known/oauth-protected-resource`,
  `/.well-known/oauth-authorization-server`, `/token`, `/revoke`, `/authorize`,
  or `/login`.
- **Inline master key material**, in either transport.
- **Unsafe key material**: a key file that is group- or world-readable, reached
  through a symlinked path component, malformed, or of the wrong length.
- **A secret digest file** that is unreadable or not owner-only.
- **A configured OAuth client that cannot be reconciled** into the database, or
  that an operator disabled there.
- **A configured tool name that this build does not register**, in either name
  list.

## Reading the effective configuration

```sh
garmin-mcp doctor
```

`doctor` renders the effective configuration through its redacted
representation, and checks the key material, the token store, and — remotely —
the database. It creates nothing, so it reports what is missing rather than
provisioning it. Secret material prints as `[redacted]`, and an unset secret as
`[unset]`, so an operator can tell "present, not shown" from "absent".

## Example configurations

Local stdio, defaults everywhere except the tool tier:

```yaml
transport: stdio
enable-write-tools: true
```

This exposes write tools to the local MCP client. Add
`enable-destructive-tools: true` to expose destructive tools to a client that
declares elicitation support; every destructive call still requires confirmation.

Remote, TLS terminated by this process:

```yaml
transport: streamable-http
bind-address: 0.0.0.0:8443
public-url: https://mcp.example.invalid/mcp
tls-cert-file: /etc/garmin-mcp/tls.crt
tls-key-file: /etc/garmin-mcp/tls.key
state-dir: /data
master-key-file: /data/keys/key-v1.json
database-path: /data/garmin.db
allowed-origins:
  - https://client.example.invalid
oauth-clients:
  - id: example-desktop-client
    name: Example Desktop Client
    redirect-uris:
      - http://127.0.0.1:33418/callback
    scopes:
      - garmin:read
    resources:
      - https://mcp.example.invalid/mcp
    public: true
```

Remote, TLS terminated by a trusted proxy:

```yaml
transport: streamable-http
bind-address: 0.0.0.0:8080
public-url: https://mcp.example.invalid/mcp
trusted-proxy-cidrs:
  - 10.0.0.0/8
state-dir: /data
master-key-file: /data/keys/key-v1.json
database-path: /data/garmin.db
oauth-clients:
  - id: example-desktop-client
    redirect-uris:
      - http://127.0.0.1:33418/callback
    scopes:
      - garmin:read
    resources:
      - https://mcp.example.invalid/mcp
    public: true
```

See [operations.md](operations.md) for what these settings mean in a deployment.

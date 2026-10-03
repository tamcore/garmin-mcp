# garmin-mcp

A Model Context Protocol server for Garmin Connect, written in Go.

It exposes Garmin Connect data — activities, wellness summaries, devices, and
profile — as MCP tools, in two shapes:

- **local stdio**, serving one Garmin account bound to the process;
- **remote Streamable HTTP**, serving many accounts, each one addressed by its
  own OAuth 2.1 authorization.

It ships as a single static binary and as a distroless container image.

## Warning: Garmin Connect is an unofficial API

Garmin publishes no public API for this data. Everything this server calls is a
private, undocumented endpoint of the Garmin Connect web application.

- Endpoints, response schemas, and WAF behavior can change at any time, without
  notice and without a deprecation period. A tool that works today can fail
  tomorrow.
- Use of these endpoints may conflict with Garmin's terms of service. That is
  your decision to make and your risk to carry.
- This project is not affiliated with, endorsed by, or supported by Garmin.
- Your Garmin credentials are entered only in a login form served by this process
  or at its terminal prompt. They are never accepted as a flag, an environment
  variable, a configuration key, or an MCP tool argument, and they are never
  stored — only the resulting Garmin token set is, encrypted.

## Local quick start (stdio)

### 1. Link a Garmin account

```sh
garmin-mcp auth
```

This starts a one-shot login page on a loopback address with a kernel-chosen
port, opens it in your browser, and prints the URL as well. Enter your Garmin
email and password there, and the one-time code if your account uses MFA. The
page stops as soon as the login succeeds, is cancelled, or expires after ten
minutes.

The resulting Garmin token set is written encrypted under the state directory. No
password is stored.

Variants:

```sh
garmin-mcp auth --no-browser   # print the URL, do not open a browser
garmin-mcp auth --tty          # read the credentials at the terminal, echo off
```

The terminal flow needs an attached terminal and refuses a pipe, because reading
a password from a pipe is how a credential ends up in a log or a CI transcript.

### 2. Check the deployment

```sh
garmin-mcp doctor
```

It reports the effective configuration (secrets redacted), the state directory,
the key material, the token store, and whether an account is linked. It creates
nothing.

### 3. Serve

```sh
garmin-mcp serve
```

Standard output carries MCP frames only. Logs, diagnostics, and errors go to
standard error.

State lives in the platform's per-user configuration directory under
`garmin-mcp/`, or wherever `--state-dir` points. The encryption key is created on
the first `auth` or `serve` run, owner-only.

## Registering the server with an MCP client

### stdio

Most MCP clients take a command. The general shape:

```json
{
  "mcpServers": {
    "garmin": {
      "command": "garmin-mcp",
      "args": ["serve"]
    }
  }
}
```

Run `garmin-mcp auth` once, outside the client, before the client starts the
server. The server does not prompt for credentials over MCP and has no login
tool.

### Remote

A remote deployment is an OAuth-protected MCP endpoint. Point the client at the
public URL and let it discover the rest:

```json
{
  "mcpServers": {
    "garmin": {
      "type": "http",
      "url": "https://mcp.example.invalid/mcp"
    }
  }
}
```

The client must be registered with the deployment first — see below — and its
redirect URI must match that registration byte for byte, unless the operator has
opted into a trailing-path wildcard (see
[docs/configuration.md](docs/configuration.md#redirect-wildcards)). The first
connection sends the user through a browser flow that logs in to Garmin and asks
for consent, then returns an access token.

## Remote deployment in brief

```yaml
transport: streamable-http
bind-address: 0.0.0.0:8443
public-url: https://mcp.example.invalid/mcp
tls-cert-file: /etc/garmin-mcp/tls.crt
tls-key-file: /etc/garmin-mcp/tls.key
state-dir: /data
master-key-file: /data/keys/key-v1.json
database-path: /data/garmin.db
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

```sh
garmin-mcp --config /etc/garmin-mcp/config.yaml serve
```

A remote deployment starts only with an `https` public URL, TLS material or
trusted proxy networks for a non-loopback bind, at least one registered OAuth
client, and an owner-only master key file.

Read [docs/operations.md](docs/operations.md) before running this in production.
Every setting is listed in [docs/configuration.md](docs/configuration.md).

## Compatibility

garmin-mcp is a tool-compatible server for the
[Taxuspt/garmin_mcp](https://github.com/Taxuspt/garmin_mcp) project. Its tool
surface is pinned by a golden snapshot test. It also serves the 5 upstream workout resources. Deliberate differences:

- `set_fit_download_dir` is not registered: it writes to the server filesystem at
  a caller's direction.
- Tools that upstream gives a filesystem path take the file content instead
  (`upload_course` takes `gpx_content`).
- There is no login tool. Credentials never become tool arguments.
- 18 tools are added beyond the pinned manifest, including `server_info`.

`garmin-mcp tools list` prints the registered tools with their tier and effect.
It needs no Garmin account, no token and no database:

```console
$ garmin-mcp tools list | grep ' tools:'
155 tools: 109 read-only, 37 write, 9 destructive
```

## Safety defaults

Write and destructive tools are off by default. `enable-write-tools` and
`enable-destructive-tools` both default to false, and destructive requires write.
On stdio those explicit operator flags authorize their tiers. Remotely the flag
is only half of the gate: the caller's OAuth grant must also carry the matching
scope. A destructive call additionally requires explicit client confirmation and
fails closed when it cannot obtain one. Write tools need no confirmation; local
operators should use the allowlist, denylist, and safety delay to narrow exposure.

A remote deployment runs as a single active instance and does not scale
horizontally. See the last section of [docs/operations.md](docs/operations.md).

## Documentation

| Document | Contents |
|----------|----------|
| [docs/configuration.md](docs/configuration.md) | Every setting, its flag, environment variable, default, and validation |
| [docs/operations.md](docs/operations.md) | Deployment, clients, database, keys, revocation, upgrades, metrics |
| [SECURITY.md](SECURITY.md) | Disclosure process, supported versions, data held, deployment checklist |
| [docs/threat-model.md](docs/threat-model.md) | Assets, adversaries, and the decisions that follow |

## Security

Report a vulnerability privately. Do not open a public issue. See
[SECURITY.md](SECURITY.md).

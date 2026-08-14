-- 0001_initial.sql — the whole v1 record set for the SQLite backend.
--
-- Conventions, applied everywhere below:
--
--   * Every timestamp is TEXT holding RFC 3339 in UTC ("2026-08-14T12:00:00Z"),
--     so string ordering equals time ordering and comparisons work in SQL.
--   * Every identifier is TEXT. Principal ids are random internal UUIDs.
--   * Nothing in this schema stores a credential in the clear. Token material is
--     present only as a keyed-HMAC lookup value (a *_hash column). Garmin token
--     sets and the Garmin identity linkage are present only as AEAD envelopes
--     produced by internal/cryptostore (a *_sealed column).
--   * No column holds health data, workout data, or a coordinate.
--   * Deliberately no index or unique constraint uses an email address. Email is
--     login and display data, never the isolation key.

-- schema_meta is a single row carrying database-wide state.
CREATE TABLE schema_meta (
    id                     INTEGER PRIMARY KEY CHECK (id = 1),
    -- The cryptostore key version every envelope in this database was sealed
    -- under at creation time. Individual rows carry their own key_version so a
    -- staged rotation can re-seal row by row; this is the database-wide default.
    encryption_key_version INTEGER NOT NULL CHECK (encryption_key_version > 0),
    -- The AEAD-sealed root from which every keyed-HMAC lookup key is derived.
    -- It never leaves this process in the clear and is not a cryptostore key.
    index_root_sealed      BLOB    NOT NULL,
    created_at             TEXT    NOT NULL
) STRICT;

-- principals is the isolation boundary. Nothing else in this schema may be used
-- to scope a query to a user.
CREATE TABLE principals (
    -- A random internal UUID. Never derived from an email or a Garmin id, so a
    -- change of either does not move the isolation key.
    id                     TEXT PRIMARY KEY,
    -- Normalized email, for login lookup and display only. UNIQUE so two
    -- accounts cannot share a login handle, but never an isolation key.
    email_normalized       TEXT UNIQUE,
    -- Keyed HMAC of the stable Garmin account identifier, under a purpose-derived
    -- key. UNIQUE is the concurrency contract: the same Garmin account cannot
    -- silently become two principals, because the second inserter loses here.
    garmin_account_hash    TEXT UNIQUE,
    -- The AEAD-sealed Garmin identity linkage (the account id and display name as
    -- Garmin reports them). Present only when the account is linked.
    garmin_identity_sealed BLOB,
    -- The cryptostore key version garmin_identity_sealed was sealed under.
    key_version            INTEGER NOT NULL CHECK (key_version > 0),
    created_at             TEXT    NOT NULL,
    updated_at             TEXT    NOT NULL
) STRICT;

-- garmin_token_sets holds one versioned, encrypted Garmin DI token set per
-- principal. The version column is the compare-and-set counter that keeps a
-- rotated refresh token from being lost.
CREATE TABLE garmin_token_sets (
    principal_id  TEXT PRIMARY KEY REFERENCES principals (id) ON DELETE CASCADE,
    -- The payload layout inside the envelope, independent of the SQL schema.
    record_schema INTEGER NOT NULL CHECK (record_schema > 0),
    -- Compare-and-set counter. Starts at 1 and only increases.
    version       INTEGER NOT NULL CHECK (version > 0),
    key_version   INTEGER NOT NULL CHECK (key_version > 0),
    sealed        BLOB    NOT NULL,
    updated_at    TEXT    NOT NULL
) STRICT;

-- oauth_clients are the MCP clients registered with this authorization server.
CREATE TABLE oauth_clients (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    -- Keyed HMAC of the client secret, or NULL for a public client. The secret
    -- itself is never stored.
    secret_hash TEXT,
    is_public   INTEGER NOT NULL CHECK (is_public IN (0, 1)),
    created_at  TEXT NOT NULL,
    disabled_at TEXT
) STRICT;

-- oauth_client_redirect_uris holds the exact redirect URIs of a client. Matching
-- is exact string equality against a row here: no prefix rule, no wildcard, no
-- normalization at match time.
CREATE TABLE oauth_client_redirect_uris (
    client_id    TEXT NOT NULL REFERENCES oauth_clients (id) ON DELETE CASCADE,
    redirect_uri TEXT NOT NULL,
    PRIMARY KEY (client_id, redirect_uri)
) STRICT;

-- consents is one row per principal and client. Revocation is recorded, never
-- deleted, so an audit trail survives a re-grant.
CREATE TABLE consents (
    principal_id TEXT NOT NULL REFERENCES principals (id) ON DELETE CASCADE,
    client_id    TEXT NOT NULL REFERENCES oauth_clients (id) ON DELETE CASCADE,
    -- Space-separated scope list, in the order granted.
    scopes       TEXT NOT NULL,
    granted_at   TEXT NOT NULL,
    revoked_at   TEXT,
    PRIMARY KEY (principal_id, client_id)
) STRICT;

-- auth_transactions is server-side authorization request state. The handle the
-- browser carries is present only as a keyed HMAC.
CREATE TABLE auth_transactions (
    handle_hash           TEXT PRIMARY KEY,
    client_id             TEXT NOT NULL REFERENCES oauth_clients (id) ON DELETE CASCADE,
    -- NULL until the browser flow has identified the principal.
    principal_id          TEXT REFERENCES principals (id) ON DELETE CASCADE,
    redirect_uri          TEXT NOT NULL,
    scopes                TEXT NOT NULL,
    code_challenge        TEXT NOT NULL,
    code_challenge_method TEXT NOT NULL CHECK (code_challenge_method = 'S256'),
    created_at            TEXT NOT NULL,
    expires_at            TEXT NOT NULL
) STRICT;

CREATE INDEX idx_auth_transactions_expires_at ON auth_transactions (expires_at);

-- auth_codes is one row per issued authorization code. Single use is enforced by
-- consumed_at, set inside the same transaction that reads the row.
CREATE TABLE auth_codes (
    code_hash      TEXT PRIMARY KEY,
    principal_id   TEXT NOT NULL REFERENCES principals (id) ON DELETE CASCADE,
    client_id      TEXT NOT NULL REFERENCES oauth_clients (id) ON DELETE CASCADE,
    redirect_uri   TEXT NOT NULL,
    scopes         TEXT NOT NULL,
    audience       TEXT NOT NULL,
    code_challenge TEXT NOT NULL,
    created_at     TEXT NOT NULL,
    expires_at     TEXT NOT NULL,
    consumed_at    TEXT
) STRICT;

CREATE INDEX idx_auth_codes_expires_at ON auth_codes (expires_at);

-- token_families groups every token descended from one authorization grant.
-- Revocation happens here, so revoking a family revokes its whole descent in one
-- statement.
CREATE TABLE token_families (
    id                TEXT PRIMARY KEY,
    principal_id      TEXT NOT NULL REFERENCES principals (id) ON DELETE CASCADE,
    client_id         TEXT NOT NULL REFERENCES oauth_clients (id) ON DELETE CASCADE,
    created_at        TEXT NOT NULL,
    revoked_at        TEXT,
    -- A short reason code, never free text from a request.
    revocation_reason TEXT
) STRICT;

CREATE INDEX idx_token_families_principal ON token_families (principal_id);
CREATE INDEX idx_token_families_client ON token_families (principal_id, client_id);

-- mcp_tokens holds the opaque MCP access and refresh token material as keyed
-- HMAC lookup values. The token itself is never stored and cannot be recovered
-- from this table.
CREATE TABLE mcp_tokens (
    token_hash  TEXT PRIMARY KEY,
    family_id   TEXT NOT NULL REFERENCES token_families (id) ON DELETE CASCADE,
    kind        TEXT NOT NULL CHECK (kind IN ('access', 'refresh')),
    scopes      TEXT NOT NULL,
    audience    TEXT NOT NULL,
    issued_at   TEXT NOT NULL,
    expires_at  TEXT NOT NULL,
    -- Set when a refresh token is redeemed. A second redemption of the same row
    -- is reuse and revokes the family.
    consumed_at TEXT,
    revoked_at  TEXT
) STRICT;

CREATE INDEX idx_mcp_tokens_family ON mcp_tokens (family_id);
CREATE INDEX idx_mcp_tokens_expires_at ON mcp_tokens (expires_at);

-- audit_events records security-relevant decisions. It carries no credential, no
-- health payload and no coordinate: the only free-ish column is detail, and the
-- store validates it against a short reason-code grammar before insert.
CREATE TABLE audit_events (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    occurred_at  TEXT NOT NULL,
    kind         TEXT NOT NULL,
    outcome      TEXT NOT NULL CHECK (outcome IN ('allowed', 'denied', 'error')),
    principal_id TEXT,
    client_id    TEXT,
    detail       TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX idx_audit_events_occurred_at ON audit_events (occurred_at);

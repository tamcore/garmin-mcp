-- 0002_oauth_contract.sql — the columns and the key width the OAuth server's storage
-- contract needs, which 0001 does not carry.
--
-- Every change here is additive except the consent key, which is widened by rebuilding
-- the table: SQLite cannot alter a primary key in place. The rebuild copies every
-- existing row into the wide key with an empty redirect URI and an empty resource, which
-- is exactly what a row written under the narrow key meant, so no grant is lost and none
-- is silently widened.
--
-- The conventions of 0001 still hold: timestamps are RFC 3339 in UTC, identifiers are
-- TEXT, nothing that is a credential is stored in the clear, and the only new
-- credential-shaped value — the client's opaque state — is present only as an AEAD
-- envelope produced by internal/cryptostore.

-- The compare-and-set counter for an authorization transaction. It starts at 0, which is
-- what a freshly created transaction is, so the default is correct for every row that
-- already exists.
ALTER TABLE auth_transactions ADD COLUMN version INTEGER NOT NULL DEFAULT 0;

-- The RFC 8707 resource indicator the authorization request named. An empty string is a
-- request that named no resource, which is a state and not a missing value, so the column
-- is NOT NULL with an empty default rather than nullable.
ALTER TABLE auth_transactions ADD COLUMN resource TEXT NOT NULL DEFAULT '';

-- The client's opaque state, sealed. It belongs to someone else and is echoed back byte
-- for byte, so it is stored the way every other value belonging to a third party is
-- stored here: as an envelope, never in the clear. NULL means the request carried no
-- state.
ALTER TABLE auth_transactions ADD COLUMN client_state_sealed BLOB;

-- The cryptostore key version client_state_sealed was sealed under, so a staged rotation
-- can re-seal row by row exactly as it can for the Garmin records.
ALTER TABLE auth_transactions ADD COLUMN client_state_key_version INTEGER;

-- The resource a family was issued for. Revocation is keyed on the consent tuple, which
-- includes the resource, so without this column revoking consent for one resource would
-- revoke the families of every other resource the same client holds.
ALTER TABLE token_families ADD COLUMN resource TEXT NOT NULL DEFAULT '';

CREATE INDEX idx_token_families_resource ON token_families (principal_id, client_id, resource);

-- How many rotations deep in its family a token is. The first pair of a family is
-- generation 0, which is what the default gives every row that already exists.
ALTER TABLE mcp_tokens ADD COLUMN generation INTEGER NOT NULL DEFAULT 0 CHECK (generation >= 0);

-- consents, rebuilt on the wide key.
--
-- The narrow (principal_id, client_id) key is a confused-deputy hazard: four grants that
-- differ only in redirect URI or resource collapse onto one row, so saving one overwrites
-- the others and revoking one over-revokes. The key below is the tuple the MCP guidance
-- names, and a change to any part of it finds no row and forces a fresh decision.
CREATE TABLE consents_wide_key (
    principal_id TEXT NOT NULL REFERENCES principals (id) ON DELETE CASCADE,
    client_id    TEXT NOT NULL REFERENCES oauth_clients (id) ON DELETE CASCADE,
    -- The exact redirect URI the grant was made against. Empty means the grant was
    -- recorded without one, which is what every row migrated from 0001 carries.
    redirect_uri TEXT NOT NULL,
    -- The RFC 8707 resource indicator. Empty means no resource was named.
    resource     TEXT NOT NULL,
    -- Space-separated scope list, in the order granted. Empty is a legal grant: a client
    -- may be authorized with no scope at all.
    scopes       TEXT NOT NULL,
    granted_at   TEXT NOT NULL,
    revoked_at   TEXT,
    PRIMARY KEY (principal_id, client_id, redirect_uri, resource)
) STRICT;

INSERT INTO consents_wide_key
    (principal_id, client_id, redirect_uri, resource, scopes, granted_at, revoked_at)
SELECT principal_id, client_id, '', '', scopes, granted_at, revoked_at FROM consents;

DROP TABLE consents;

ALTER TABLE consents_wide_key RENAME TO consents;

-- The lookup that is not the primary key: "does this principal still authorize this
-- client at all", which every token read asks.
CREATE INDEX idx_consents_principal_client ON consents (principal_id, client_id);

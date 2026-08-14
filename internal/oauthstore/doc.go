// Package oauthstore adapts the SQLite backend in internal/store to the storage
// interfaces internal/oauthserver declares.
//
// # Why the package exists
//
// The authorization server declares what it needs from persistence and must not
// depend on a database; the store implements equivalent operations in its own
// vocabulary and must not depend on its consumer. Neither may import the other, so
// something has to sit between them. That is this package, and its whole point is
// the assertion in adapter.go: when the two sides stop fitting, the build breaks,
// instead of a comment quietly going out of date.
//
// # What the adapter does not do
//
// Clients are not read from SQLite. They are operator-registered configuration, so
// [New] takes a client source and delegates Client to it. The database holds no
// client scope set, no allowed resource indicators and no SHA-256 secret digest,
// and inventing them here would be worse than the dependency it saves.
//
// # How a credential is addressed
//
// The authorization server hands over a lookup: the SHA-256 digest of a credential
// it minted, never the credential. The store takes credential material and derives
// its own keyed-HMAC lookup value under a per-purpose key. The adapter bridges the
// two by passing the hex digest as the store's material, on every write and every
// read, so the store hashes a value that is already a digest. Nothing is weakened
// by that — the preimage is still 256 bits of crypto/rand — and no credential ever
// reaches this package. The digest is re-attached to every record returned, because
// the store does not keep it.
//
// A zero lookup is refused everywhere. It is the digest of an absent credential,
// and a request that presented nothing must never address a row.
//
// # Field mapping
//
// Where the two vocabularies differ, the mapping is:
//
//	oauthserver                       store
//	-----------                       -----
//	Lookup                            Secret(lookup.Hex()), re-attached on read
//	identity.Principal                PrincipalID string (identity.NewPrincipal on read)
//	Transaction.Stage                 derived: no principal id is StagePending,
//	                                  a principal id is StageAuthenticated
//	Transaction.State                 AuthTransaction.ClientState, sealed by the store
//	Transaction.Version               auth_transactions.version, the CAS counter
//	Resource                          Audience on a code, Resource on a family;
//	                                  the zero Resource is the empty string
//	ScopeSet                          space-separated scope list
//	FamilyID                          TokenFamilyGrant.FamilyID, supplied not minted
//	RefreshToken.Generation           mcp_tokens.generation
//	AccessToken/RefreshToken.IssuedAt absolute instants, not lifetimes
//
// # What the store demands that the interfaces do not say
//
//   - A transaction and a code reference a client row and a principal row by
//     foreign key, so both must already exist.
//   - SaveTokenPair needs an active consent for the principal and client. Call
//     SaveConsent first; the exact-key check stays in the authorization decision,
//     because an access token record carries no redirect URI.
//   - A code needs a non-empty resource, because the audience column is required.
//
// # Errors
//
// Every store failure is translated: the sentinel the authorization server branches
// on is wrapped in front of the store's own error, so errors.Is finds the protocol
// meaning and the cause stays reachable for the log. Anything with no protocol
// meaning arrives as ErrStorage. No message here carries a token, a code, a
// capability or client state.
package oauthstore

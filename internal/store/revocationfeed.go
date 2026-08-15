package store

// The revocation feed.
//
// A revocation that is only recorded is a revocation an already-open session
// survives until its next request. The cascades therefore announce what they
// withdrew, and a composition root turns that announcement into the transport's
// session termination.
//
// Two rules keep the feed from becoming a second, weaker source of truth:
//
//   - An event is published only after the transaction that caused it committed.
//     A rolled-back cascade announces nothing, because a session closed for a
//     revocation that did not happen is a disconnection with no authorization
//     change behind it.
//   - The database remains the authority. An event is a hint that something was
//     withdrawn; every authorization decision is still made by reading a row.

// Revocation reason codes, as they are written to token_families.revocation_reason
// and carried on a RevocationEvent.
//
// They are exported because a caller has to name one when it revokes a family, and
// because a consumer of the feed reads them. They are a closed vocabulary of
// lowercase snake-case codes: nothing from a request ever becomes one.
const (
	// ReasonConsentRevoked means a grant was withdrawn, by the user, by an
	// operator, or by a wider principal-level revocation.
	ReasonConsentRevoked = "consent_revoked"

	// ReasonRefreshReuse means a refresh token was replayed, which revokes the
	// whole family it belonged to.
	ReasonRefreshReuse = "refresh_token_reuse"

	// ReasonGarminUnlinked means the principal's Garmin linkage was removed, which
	// takes down everything that depended on it.
	ReasonGarminUnlinked = "garmin_unlinked"
)

// A RevocationEvent names an authorization this store withdrew.
//
// Every field is a selector and an empty field means "not narrowed by this": an
// event carrying only a principal covers everything that principal holds. A
// consumer must treat the event as at-least-once, and as possibly wider than the
// exact rows that changed — the store publishes what the cascade was keyed on, and
// a cascade keyed on a resource is reported by its principal and client, which is
// the safe direction because it closes more sessions rather than fewer.
//
// It carries no token material, no email and no free text: Reason is one of the
// exported reason codes above.
type RevocationEvent struct {
	// PrincipalID is the principal whose authorization was withdrawn, or "" when
	// the cascade was not keyed on one.
	PrincipalID string

	// ClientID is the OAuth client the withdrawal covers, or "" for every client.
	ClientID string

	// FamilyID is the token family that was revoked, or "" when the cascade was
	// wider than one family.
	FamilyID string

	// Reason is one of the exported reason codes.
	Reason string
}

// isEmpty reports whether an event names nothing at all. Such an event is never
// published: it would select every session in the deployment, so a defect in a
// cascade must not be able to express it.
func (e RevocationEvent) isEmpty() bool {
	return e.PrincipalID == "" && e.ClientID == "" && e.FamilyID == ""
}

// A RevocationSink receives revocation events from the store.
//
// The interface lives with its producer because the store is what knows a
// revocation happened. An implementation must not block: it is called on the
// goroutine that ran the cascade, immediately after the commit, so a slow consumer
// would hold up the caller that revoked. Bounding and dropping belong to the
// implementation, never here.
type RevocationSink interface {
	// PublishRevocation announces one committed revocation. It must return
	// promptly and must not panic.
	PublishRevocation(event RevocationEvent)
}

// publishRevocation announces a committed revocation, if a sink is wired and the
// event names something.
func (s *SQLiteStore) publishRevocation(event RevocationEvent) {
	if s.revocations == nil || event.isEmpty() {
		return
	}
	s.revocations.PublishRevocation(event)
}

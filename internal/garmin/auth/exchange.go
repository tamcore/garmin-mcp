package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
)

// diTokenResponse is the DI OAuth2 token payload. Decoding is tolerant on
// purpose: Garmin adds fields, and an unknown field must not fail an otherwise
// usable response.
type diTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
}

// tokenClient performs the calls that turn a login into a usable DI token set:
// the token endpoint on the DI auth host and the session validation call on the
// API tier. It holds no mutable state, so it is safe to share.
type tokenClient struct {
	hosts protocol.Hosts
	doer  Doer
	clock Clock
}

// response turns a raw response into the classifier's input, anchored at the
// injected clock so a Retry-After HTTP-date is interpreted deterministically.
func (c tokenClient) response(raw rawResponse) protocol.Response {
	return protocol.NewResponseFromParts(raw.status, "", raw.header, raw.body).WithNow(c.clock.Now())
}

// exchangeTicket trades a CAS service ticket for a DI token set, trying the
// candidate DI client ids in the pinned order until one is accepted.
//
// A rate limit stops the loop: hammering the remaining candidates would deepen
// it. Source: _exchange_service_ticket (client.py, 0.3.10), which raises on 429
// and continues past any other non-2xx.
func (c tokenClient) exchangeTicket(ctx context.Context, ticket, serviceURL string) (TokenSet, error) {
	sess, err := newSession(c.doer)
	if err != nil {
		return TokenSet{}, err
	}

	var lastErr error
	for _, clientID := range protocol.DIClientIDs() {
		form := url.Values{
			"client_id":      {clientID},
			"service_ticket": {ticket},
			"grant_type":     {protocol.DIGrantTypeServiceTicket},
			"service_url":    {serviceURL},
		}

		set, err := c.postToken(ctx, sess, protocol.OpExchangeServiceTicket, clientID, form)
		if err == nil {
			return set, nil
		}
		if errors.Is(err, protocol.ErrRateLimited) {
			return TokenSet{}, err
		}
		lastErr = err
	}

	if lastErr == nil {
		lastErr = ErrMalformedTokenResponse
	}
	return TokenSet{}, fmt.Errorf("garmin auth: %w: %w", ErrTokenExchangeFailed, lastErr)
}

// refresh exchanges a stored refresh token for a rotated DI token set, reusing
// the client id the token was issued to. Source: _refresh_di_token.
func (c tokenClient) refresh(ctx context.Context, set TokenSet) (TokenSet, error) {
	if set.RefreshToken() == "" || set.ClientID() == "" {
		return TokenSet{}, ErrNoRefreshToken
	}

	sess, err := newSession(c.doer)
	if err != nil {
		return TokenSet{}, err
	}

	form := url.Values{
		"grant_type":    {protocol.DIGrantTypeRefreshToken},
		"client_id":     {set.ClientID()},
		"refresh_token": {set.RefreshToken()},
	}

	rotated, err := c.postToken(ctx, sess, protocol.OpRefreshToken, set.ClientID(), form)
	if err != nil {
		if isRejectedRefresh(err) {
			return TokenSet{}, fmt.Errorf("garmin auth: %w: %w", ErrRefreshRejected, err)
		}
		return TokenSet{}, err
	}
	// Carry the previous refresh token forward when Garmin omits a new one, and
	// keep the previous client id when the rotated token names none.
	next := set.WithRotated(rotated.Token(), rotated.RefreshToken(), rotated.ExpiresAt())
	if rotated.ClientID() != "" {
		next = next.WithClientID(rotated.ClientID())
	}
	return next, nil
}

func isRejectedRefresh(err error) bool {
	var protocolErr *protocol.Error
	if !errors.As(err, &protocolErr) {
		return false
	}
	return protocolErr.Op == protocol.OpRefreshToken &&
		protocolErr.Endpoint == protocol.EndpointDIToken &&
		(protocolErr.Status == http.StatusBadRequest || protocolErr.Status == http.StatusUnauthorized)
}

// postToken performs one DI token endpoint call and decodes its response.
func (c tokenClient) postToken(
	ctx context.Context,
	sess *session,
	op protocol.Op,
	clientID string,
	form url.Values,
) (TokenSet, error) {
	header := protocol.NativeAPIHeaders()
	header.Set("Authorization", protocol.BasicAuthHeader(clientID))
	header.Set("Accept", "application/json,text/html;q=0.9,*/*;q=0.8")
	header.Set("Cache-Control", "no-cache")

	raw, err := sess.postForm(ctx, c.hosts.DITokenURL(), header, form)
	if err != nil {
		return TokenSet{}, transportError(op, protocol.EndpointDIToken, err)
	}
	if err := statusError(op, protocol.EndpointDIToken, c.response(raw)); err != nil {
		return TokenSet{}, err
	}

	var payload diTokenResponse
	if err := json.Unmarshal(raw.body, &payload); err != nil || payload.AccessToken == "" {
		// A decoder error would quote the body, which carries token material.
		return TokenSet{}, &protocol.Error{
			Op:       op,
			Endpoint: protocol.EndpointDIToken,
			Status:   raw.status,
			Outcome:  protocol.OutcomeUnknown,
			Err:      ErrMalformedTokenResponse,
		}
	}

	return tokenSetFrom(payload, clientID), nil
}

// tokenSetFrom labels a token response with the client id and the expiry it
// claims. Both claims come from an unverified JWT: they are scheduling and
// labeling metadata, never authorization.
func tokenSetFrom(payload diTokenResponse, fallbackClientID string) TokenSet {
	clientID := fallbackClientID
	if claimed, ok := UnverifiedClientID(payload.AccessToken); ok {
		clientID = claimed
	}

	var expiresAt time.Time
	if claimed, ok := UnverifiedExpiry(payload.AccessToken); ok {
		expiresAt = claimed
	}

	return NewTokenSet(payload.AccessToken, payload.RefreshToken, clientID, expiresAt)
}

// socialProfileResponse is the part of the API-tier profile this package reads.
//
// Decoding is tolerant on purpose, exactly as it is for the token response: Garmin
// adds fields, and an unknown one must not fail a session that is otherwise valid.
// The id is a json.Number because Garmin reports it as a number today and a string
// is an equally legal spelling of the same identifier.
type socialProfileResponse struct {
	ProfileID   json.Number `json:"profileId"`
	DisplayName string      `json:"displayName"`
}

// account projects the decoded profile onto the identity a caller may key on.
//
// The numeric profile id is the stable account identifier and is preferred.
// Garmin's displayName is a stable per-account handle as well, so it is the
// fallback when a response carries no profile id; a profile with neither yields the
// zero account, and it is the caller that decides whether it can proceed without
// one.
func (p socialProfileResponse) account() garminAccount {
	id := strings.TrimSpace(p.ProfileID.String())
	if id == "" {
		id = strings.TrimSpace(p.DisplayName)
	}
	return garminAccount{accountID: id, displayName: p.DisplayName}
}

// validateSession proves the API tier accepts a candidate session before it is
// stored, and reports the account the API tier says that session belongs to.
//
// The verdict distinguishes a rejection from a temporary failure: 401 and 403
// mean the token is not accepted for this account or region, and anything else
// non-2xx is inconclusive. A caller must not read a temporary failure as a
// rejection. Source: Client._verify_token and upstream issue #369.
//
// The identity comes from this response and from nowhere else. It is the one
// account claim in the whole login that a Garmin-authenticated call produced, so it
// is the only one a deployment may key isolation on — and reading it here costs no
// extra request, because the body is already on the wire.
//
// A body that does not decode is not a rejection: the session was accepted, so the
// account is reported as absent and the login stands. The decoder error is
// deliberately dropped rather than wrapped, because it would quote the body.
func (c tokenClient) validateSession(ctx context.Context, set TokenSet) (garminAccount, error) {
	sess, err := newSession(c.doer)
	if err != nil {
		return garminAccount{}, err
	}

	header := protocol.NativeAPIHeaders()
	header.Set("Authorization", "Bearer "+set.Token())
	header.Set("Accept", "application/json")

	raw, err := sess.get(ctx, c.hosts.SocialProfileURL(), header)
	if err != nil {
		return garminAccount{}, transportError(protocol.OpValidateSession,
			protocol.EndpointSocialProfile, err)
	}

	class := protocol.ClassifySessionValidation(c.response(raw))
	if err := class.Err(protocol.OpValidateSession, protocol.EndpointSocialProfile, nil); err != nil {
		return garminAccount{}, err
	}

	var profile socialProfileResponse
	if err := json.Unmarshal(raw.body, &profile); err != nil {
		return garminAccount{}, nil
	}
	return profile.account(), nil
}

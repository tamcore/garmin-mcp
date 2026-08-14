package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
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

// validateSession proves the API tier accepts a candidate session before it is
// stored.
//
// The verdict distinguishes a rejection from a temporary failure: 401 and 403
// mean the token is not accepted for this account or region, and anything else
// non-2xx is inconclusive. A caller must not read a temporary failure as a
// rejection. Source: Client._verify_token and upstream issue #369.
func (c tokenClient) validateSession(ctx context.Context, set TokenSet) error {
	sess, err := newSession(c.doer)
	if err != nil {
		return err
	}

	header := protocol.NativeAPIHeaders()
	header.Set("Authorization", "Bearer "+set.Token())
	header.Set("Accept", "application/json")

	raw, err := sess.get(ctx, c.hosts.SocialProfileURL(), header)
	if err != nil {
		return transportError(protocol.OpValidateSession, protocol.EndpointSocialProfile, err)
	}

	class := protocol.ClassifySessionValidation(c.response(raw))
	return class.Err(protocol.OpValidateSession, protocol.EndpointSocialProfile, nil)
}

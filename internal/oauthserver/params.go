package oauthserver

// The OAuth request and response parameter names, in one place.
//
// They are protocol vocabulary: each one appears in a duplicate-parameter check, in a
// parser and often in a response, and a typo in any of those would be a silently
// weakened check rather than a compile error. Naming them once removes that class of
// mistake, which is why these are constants rather than repeated literals.
const (
	paramResponseType        = "response_type"
	paramClientID            = "client_id"
	paramClientSecret        = "client_secret"
	paramRedirectURI         = "redirect_uri"
	paramScope               = "scope"
	paramState               = "state"
	paramCodeChallenge       = "code_challenge"
	paramCodeChallengeMethod = "code_challenge_method"
	paramResource            = "resource"
	paramGrantType           = "grant_type"
	paramCode                = "code"
	paramCodeVerifier        = "code_verifier"
	paramRefreshToken        = "refresh_token"
	paramToken               = "token"
	paramTokenTypeHint       = "token_type_hint"
	paramError               = "error"
	paramErrorDescription    = "error_description"
)

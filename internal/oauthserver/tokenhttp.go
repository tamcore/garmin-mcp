package oauthserver

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"
)

// maxTokenBody bounds the token endpoint's form body. A legitimate request is a few
// hundred bytes; the bound exists so an unauthenticated caller cannot make this server
// buffer an arbitrary amount before it has decided anything.
const maxTokenBody = 8 << 10

// formContentType is the only body encoding RFC 6749 §4.1.3 defines for this endpoint.
const formContentType = "application/x-www-form-urlencoded"

// tokenSuccess is the RFC 6749 §5.1 response.
//
// This struct is one of only two places in the package that put a credential into an
// output; the other is the authorization redirect. Every other rendering path is
// redacted, which is what makes "no token reaches a log" checkable by reading the call
// sites of [Secret.Reveal].
type tokenSuccess struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope,omitempty"`
}

// tokenFailure is the RFC 6749 §5.2 error response.
type tokenFailure struct {
	Error       string `json:"error"`
	Description string `json:"error_description,omitempty"`
}

// TokenHandler serves the token endpoint.
//
// It accepts only POST with a form body, because a token request in a URL would put a code
// or a refresh token into proxy logs and browser history. Every response, success or
// failure, is marked no-store: an intermediary that cached a token response would hand the
// next caller someone else's credential.
func (s *Server) TokenHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeTokenFailure(w, http.StatusMethodNotAllowed, ErrorInvalidRequest,
				"The token endpoint accepts POST only.")
			return
		}
		if !isFormRequest(r) {
			writeTokenFailure(w, http.StatusBadRequest, ErrorInvalidRequest,
				"The request body must be "+formContentType+".")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxTokenBody)
		if err := r.ParseForm(); err != nil {
			writeTokenFailure(w, http.StatusBadRequest, ErrorInvalidRequest,
				"The request body could not be read.")
			return
		}
		s.serveTokenRequest(w, r)
	})
}

// serveTokenRequest parses, dispatches and renders. It is split out so TokenHandler stays
// a readable list of preconditions.
func (s *Server) serveTokenRequest(w http.ResponseWriter, r *http.Request) {
	req, err := ParseTokenForm(r.PostForm, r.Header)
	if err != nil {
		writeTokenFailure(w, http.StatusBadRequest, ErrorInvalidRequest,
			"The request parameters are malformed or conflicting.")
		return
	}
	response, err := s.Token(r.Context(), req)
	if err != nil {
		if tokenErr, ok := errors.AsType[*TokenError](err); ok {
			writeTokenFailure(w, tokenErr.Status(), tokenErr.Code(), tokenErr.Description())
			return
		}
		writeTokenFailure(w, http.StatusInternalServerError, ErrorServerError,
			"The request could not be completed.")
		return
	}
	writeJSON(w, http.StatusOK, tokenSuccess{
		AccessToken:  response.AccessToken.Reveal(),
		TokenType:    response.TokenType,
		ExpiresIn:    response.ExpiresIn,
		RefreshToken: response.RefreshToken.Reveal(),
		Scope:        response.Scopes.String(),
	})
}

// isFormRequest checks the media type, ignoring parameters such as a charset.
func isFormRequest(r *http.Request) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && mediaType == formContentType
}

func writeTokenFailure(w http.ResponseWriter, status int, code, description string) {
	writeJSON(w, status, tokenFailure{Error: code, Description: description})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	encoded, err := json.Marshal(body)
	if err != nil {
		http.Error(w, "{}", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(encoded)
}

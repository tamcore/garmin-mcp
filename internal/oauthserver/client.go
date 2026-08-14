package oauthserver

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// Client registration bounds.
const (
	// MaxClientIDLen bounds a client identifier.
	MaxClientIDLen = 256
	// MaxClientNameLen bounds the human-readable name shown on the disclosure
	// page. It is display material and must not be able to flood a page or a log.
	MaxClientNameLen = 128
	// MaxRedirectURIsPerClient bounds how many redirect URIs one client may
	// register.
	MaxRedirectURIsPerClient = 8
	// MaxResourcesPerClient bounds how many resource indicators one client may be
	// permitted to request.
	MaxResourcesPerClient = 8
)

// An AuthMethod is a token endpoint client authentication method.
type AuthMethod string

// The token endpoint authentication methods this server implements. There is no
// private_key_jwt and no client_secret_jwt: neither is needed by an MCP client,
// and each would add a signature-verification path for no benefit.
const (
	// AuthMethodNone is a public client. It is safe here only because PKCE S256 is
	// mandatory on every authorization request, so possession of the code is not
	// sufficient to redeem it.
	AuthMethodNone AuthMethod = "none"
	// AuthMethodSecretBasic sends the client secret in the Authorization header.
	AuthMethodSecretBasic AuthMethod = "client_secret_basic"
	// AuthMethodSecretPost sends the client secret in the form body.
	AuthMethodSecretPost AuthMethod = "client_secret_post"
)

// IsValid reports whether m is one of the implemented methods.
func (m AuthMethod) IsValid() bool {
	switch m {
	case AuthMethodNone, AuthMethodSecretBasic, AuthMethodSecretPost:
		return true
	default:
		return false
	}
}

// A ClientSpec is the unvalidated shape of a registration, as an operator writes
// it in configuration or as the storage adapter reads it from a row. Everything is
// a plain type, so neither the configuration loader nor the storage adapter has to
// construct a validated value itself: they fill in a spec and call [NewClient].
//
// SecretHashHex is the hex-encoded SHA-256 of the client secret, never the secret,
// so this server holds no recoverable client secret at rest.
type ClientSpec struct {
	// ID is the client identifier. No vendor identifier is ever defaulted or
	// hardcoded: every client in this server exists because an operator
	// registered it.
	ID string
	// Name is the human-readable name shown on the non-binding disclosure page.
	Name string
	// RedirectURIs are the exact redirect URIs this client may use. At least one is
	// required, and matching against them is byte-exact.
	RedirectURIs []string
	// Scopes is the space-delimited maximum scope set this client may ever be
	// granted. A request for more is refused before any user sees a page.
	Scopes string
	// Resources are the exact RFC 8707 resource indicators this client may
	// request. At least one is required.
	Resources []string
	// TokenEndpointAuthMethod is one of the [AuthMethod] constants.
	TokenEndpointAuthMethod string
	// SecretHashHex is the hex SHA-256 of the client secret, required for a
	// confidential client and forbidden for a public one.
	SecretHashHex string
}

// A Client is a validated, immutable client registration.
//
// Its fields are unexported so a Client can only come from [NewClient], and
// therefore every Client in the system has already passed registration
// validation. The zero Client is invalid.
type Client struct {
	id           string
	name         string
	redirectURIs []RedirectURI
	scopes       ScopeSet
	resources    []Resource
	authMethod   AuthMethod
	secretHash   Lookup
}

// NewClient validates spec and returns the client it describes.
//
// Beyond the field-level checks, two rules are structural: a confidential client
// must carry a secret digest and a public client must not, and every redirect URI
// and resource must pass the full [ParseRedirectURI] and [ParseResource] rules, so
// an operator cannot register a wildcard, a plaintext target or a fragment.
func NewClient(spec ClientSpec) (Client, error) {
	if err := validateClientIdentity(spec); err != nil {
		return Client{}, err
	}
	redirects, err := parseClientRedirectURIs(spec.RedirectURIs)
	if err != nil {
		return Client{}, err
	}
	resources, err := parseClientResources(spec.Resources)
	if err != nil {
		return Client{}, err
	}
	scopes, err := ParseScopeSet(spec.Scopes)
	if err != nil {
		return Client{}, err
	}
	if scopes.IsEmpty() {
		return Client{}, fmt.Errorf("client %q registers no scope: %w", spec.ID, ErrInvalidClient)
	}
	secretHash, err := clientSecretHash(spec)
	if err != nil {
		return Client{}, err
	}
	return Client{
		id:           spec.ID,
		name:         spec.Name,
		redirectURIs: redirects,
		scopes:       scopes,
		resources:    resources,
		authMethod:   AuthMethod(spec.TokenEndpointAuthMethod),
		secretHash:   secretHash,
	}, nil
}

func validateClientIdentity(spec ClientSpec) error {
	if err := validateClientID(spec.ID); err != nil {
		return err
	}
	if len(spec.Name) > MaxClientNameLen {
		return fmt.Errorf("client %q has a %d byte name, the limit is %d: %w",
			spec.ID, len(spec.Name), MaxClientNameLen, ErrInvalidClient)
	}
	if strings.IndexFunc(spec.Name, unicode.IsControl) >= 0 {
		return fmt.Errorf("client %q has a control character in its name: %w",
			spec.ID, ErrInvalidClient)
	}
	if !AuthMethod(spec.TokenEndpointAuthMethod).IsValid() {
		return fmt.Errorf("client %q has token endpoint auth method %q: %w",
			spec.ID, spec.TokenEndpointAuthMethod, ErrInvalidClient)
	}
	return nil
}

// validateClientID is the shared check for a client identifier, which ends up in a log
// line, a map key and a metadata document.
func validateClientID(id string) error {
	const label = "client id"
	if id == "" {
		return fmt.Errorf("%s is empty: %w", label, ErrInvalidClient)
	}
	if strings.TrimSpace(id) != id {
		return fmt.Errorf("%s is padded with whitespace: %w", label, ErrInvalidClient)
	}
	if len(id) > MaxClientIDLen {
		return fmt.Errorf("%s is %d bytes, the limit is %d: %w",
			label, len(id), MaxClientIDLen, ErrInvalidClient)
	}
	if idx := strings.IndexFunc(id, isDisallowedIDRune); idx >= 0 {
		return fmt.Errorf("%s carries a disallowed rune at offset %d: %w",
			label, idx, ErrInvalidClient)
	}
	return nil
}

func isDisallowedIDRune(r rune) bool {
	return unicode.IsControl(r) || unicode.IsSpace(r) || r == unicode.ReplacementChar
}

func parseClientRedirectURIs(raw []string) ([]RedirectURI, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("client registers no redirect URI: %w", ErrInvalidClient)
	}
	if len(raw) > MaxRedirectURIsPerClient {
		return nil, fmt.Errorf("client registers %d redirect URIs, the limit is %d: %w",
			len(raw), MaxRedirectURIsPerClient, ErrInvalidClient)
	}
	parsed := make([]RedirectURI, 0, len(raw))
	for _, candidate := range raw {
		uri, err := ParseRedirectURI(candidate)
		if err != nil {
			return nil, err
		}
		if slices.ContainsFunc(parsed, uri.Equal) {
			return nil, fmt.Errorf("client registers a duplicate redirect URI: %w", ErrInvalidClient)
		}
		parsed = append(parsed, uri)
	}
	return parsed, nil
}

func parseClientResources(raw []string) ([]Resource, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("client registers no resource indicator: %w", ErrInvalidClient)
	}
	if len(raw) > MaxResourcesPerClient {
		return nil, fmt.Errorf("client registers %d resources, the limit is %d: %w",
			len(raw), MaxResourcesPerClient, ErrInvalidClient)
	}
	parsed := make([]Resource, 0, len(raw))
	for _, candidate := range raw {
		resource, err := ParseResource(candidate)
		if err != nil {
			return nil, err
		}
		if slices.ContainsFunc(parsed, resource.Equal) {
			return nil, fmt.Errorf("client registers a duplicate resource: %w", ErrInvalidClient)
		}
		parsed = append(parsed, resource)
	}
	return parsed, nil
}

func clientSecretHash(spec ClientSpec) (Lookup, error) {
	public := AuthMethod(spec.TokenEndpointAuthMethod) == AuthMethodNone
	switch {
	case public && spec.SecretHashHex != "":
		return Lookup{}, fmt.Errorf(
			"client %q uses auth method none but registers a secret digest: %w",
			spec.ID, ErrInvalidClient)
	case public:
		return Lookup{}, nil
	case spec.SecretHashHex == "":
		return Lookup{}, fmt.Errorf("confidential client %q registers no secret digest: %w",
			spec.ID, ErrInvalidClient)
	}
	hash, err := ParseLookup(spec.SecretHashHex)
	if err != nil {
		return Lookup{}, fmt.Errorf("client %q has a malformed secret digest: %w",
			spec.ID, ErrInvalidClient)
	}
	return hash, nil
}

// ID returns the client identifier.
func (c Client) ID() string { return c.id }

// Name returns the human-readable name for the disclosure page.
func (c Client) Name() string { return c.name }

// AuthMethod returns the registered token endpoint authentication method.
func (c Client) AuthMethod() AuthMethod { return c.authMethod }

// IsPublic reports whether the client authenticates with method none.
func (c Client) IsPublic() bool { return c.authMethod == AuthMethodNone }

// RedirectURIs returns a copy of the registered redirect URIs.
func (c Client) RedirectURIs() []RedirectURI { return slices.Clone(c.redirectURIs) }

// MaxScopes returns the widest scope set this client may ever be granted.
func (c Client) MaxScopes() ScopeSet { return c.scopes }

// Resources returns a copy of the resource indicators the client may request.
func (c Client) Resources() []Resource { return slices.Clone(c.resources) }

// MatchRedirectURI resolves a presented redirect URI against the registration.
//
// A presented value that is malformed and one that is well formed but
// unregistered both return ErrRedirectURINotRegistered, because the caller must
// treat them identically: in neither case may an error be delivered by
// redirecting to it.
func (c Client) MatchRedirectURI(presented string) (RedirectURI, error) {
	candidate, err := ParseRedirectURI(presented)
	if err != nil {
		return RedirectURI{}, fmt.Errorf("presented redirect URI is not usable: %w",
			ErrRedirectURINotRegistered)
	}
	for _, registered := range c.redirectURIs {
		if registered.Equal(candidate) {
			return registered, nil
		}
	}
	return RedirectURI{}, fmt.Errorf("client %q has no such registered redirect URI: %w",
		c.id, ErrRedirectURINotRegistered)
}

// AllowsResource reports whether the client may request tokens for resource.
func (c Client) AllowsResource(resource Resource) bool {
	return slices.ContainsFunc(c.resources, resource.Equal)
}

// Authenticate checks a presented client credential against the registration.
//
// A public client must present nothing: a public client that presents a secret is
// refused rather than waved through, because it means the client, the operator or
// an attacker is confused about which registration is in play. A confidential
// client must present a secret whose digest matches, compared in constant time.
func (c Client) Authenticate(presented Secret) error {
	if c.IsPublic() {
		if !presented.IsZero() {
			return fmt.Errorf("client %q is public but presented a secret: %w",
				c.id, ErrClientAuthFailed)
		}
		return nil
	}
	if presented.IsZero() {
		return fmt.Errorf("client %q presented no secret: %w", c.id, ErrClientAuthFailed)
	}
	if !presented.Lookup().Equal(c.secretHash) {
		return fmt.Errorf("client %q presented the wrong secret: %w", c.id, ErrClientAuthFailed)
	}
	return nil
}

// String reports the registration without its secret digest.
func (c Client) String() string {
	return "oauthserver.Client{id:" + c.id +
		" authMethod:" + string(c.authMethod) +
		" redirectURIs:" + strconv.Itoa(len(c.redirectURIs)) +
		" secret:" + presence(!c.secretHash.IsZero()) + "}"
}

// GoString satisfies the %#v verb with the same redacted rendering.
func (c Client) GoString() string { return c.String() }

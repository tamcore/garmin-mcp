package mcpserver

import "errors"

// Sentinel errors New and AddTool return. Every one is a start-up failure: a
// server that cannot be assembled correctly must not start serving.
var (
	// ErrMissingDependency reports that a required injected dependency is nil.
	// Nothing is substituted from a global or a package-level default, because a
	// silently defaulted policy or principal resolver is a security failure.
	ErrMissingDependency = errors.New("mcpserver: required dependency is missing")

	// ErrInvalidInfo reports that the server identity is incomplete.
	ErrInvalidInfo = errors.New("mcpserver: invalid server info")

	// ErrInvalidToolSpec reports a tool spec with a missing or malformed field.
	ErrInvalidToolSpec = errors.New("mcpserver: invalid tool spec")

	// ErrDuplicateTool reports that two registrars claimed the same tool name.
	ErrDuplicateTool = errors.New("mcpserver: duplicate tool name")

	// ErrInvalidResourceSpec reports a resource spec with a missing or malformed
	// field.
	ErrInvalidResourceSpec = errors.New("mcpserver: invalid resource spec")

	// ErrDuplicateResource reports that two registrars claimed the same resource URI.
	ErrDuplicateResource = errors.New("mcpserver: duplicate resource URI")

	// ErrAnnotationMismatch reports annotations that contradict the declared
	// tier, or a tool that claims a closed world. Garmin is an open-world API, and
	// a destructive tool that advertises itself as read-only would mislead every
	// client that reads the hints.
	ErrAnnotationMismatch = errors.New("mcpserver: annotations contradict the tool tier")

	// ErrInvalidHTTPOptions reports a malformed Streamable HTTP option: a public
	// URL that could not be an audience, an origin that is not a bare origin, or
	// a trusted-proxy range that is not a CIDR.
	ErrInvalidHTTPOptions = errors.New("mcpserver: invalid Streamable HTTP options")

	// ErrInsecureBind reports a cleartext public deployment. Bearer tokens travel
	// on every request, so serving them over cleartext on anything but loopback
	// publishes them; it is refused unless the development override is set.
	ErrInsecureBind = errors.New("mcpserver: refusing a cleartext public bind")
)

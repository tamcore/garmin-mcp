// Package protocol holds the Garmin Connect wire identifiers (hosts, paths,
// client identities, user agents, pacing bounds) and the login response
// classifier.
//
// Every constant carries a source comment naming the upstream behavior it
// reproduces. The reference is python-garminconnect 0.3.10
// (commit 414b54023a31259232744bb67f00a2aa71065e09), file garminconnect/client.py.
//
// The package performs no I/O and holds no mutable package-level state. All
// values are immutable: methods return new values instead of mutating their
// receiver.
//
// Secret handling: Response and Classification carry raw response material
// (body, headers, service ticket, CSRF token, page title). Both implement
// redacted String, GoString and MarshalJSON, so printing or serializing them
// never emits that material. Error renders only the package's own sanitized
// label constants plus a redacted cause; a caller that needs the real cause
// must unwrap it and apply its own redaction.
package protocol

import (
	"errors"
	"fmt"
	"strings"
)

// ErrUnsupportedDomain reports a domain outside the Garmin allowlist.
// Source: the ValueError raised by Client.__init__ for a domain that is not in
// ALLOWED_DOMAINS (client.py, 0.3.10).
var ErrUnsupportedDomain = errors.New("garmin: unsupported domain")

// Domain is the Garmin host suffix that selects an account region. Only the two
// official Garmin domains are valid; construct one with ParseDomain rather than
// converting a caller-supplied string.
type Domain string

const (
	// DomainGlobal is the default region. Source: Client.__init__ default
	// argument domain="garmin.com".
	DomainGlobal Domain = "garmin.com"

	// DomainChina serves accounts in the Garmin China user database. Source:
	// the domain-aware host construction in Client.__init__, which notes that
	// CN users do not exist in the .com user database.
	DomainChina Domain = "garmin.cn"
)

// allowedDomains is the region allowlist, in preference order.
// Source: ALLOWED_DOMAINS = {"garmin.com", "garmin.cn"} in client.py (0.3.10).
// Anything else would let a caller aim credential-bearing SSO URLs at a host of
// their choosing.
var allowedDomains = [...]Domain{DomainGlobal, DomainChina}

// AllowedDomains returns a fresh copy of the accepted domains, in the order
// ParseDomain reports them.
func AllowedDomains() []Domain {
	out := make([]Domain, len(allowedDomains))
	copy(out, allowedDomains[:])
	return out
}

// IsAllowed reports whether d is exactly one of the allowlisted domains. It does
// not trim or fold case: use ParseDomain for caller-supplied input.
func (d Domain) IsAllowed() bool {
	for _, allowed := range allowedDomains {
		if d == allowed {
			return true
		}
	}
	return false
}

// ParseDomain validates a caller-supplied region. It trims surrounding space and
// folds case; an empty value selects DomainGlobal. Any value outside the
// allowlist is rejected with an error wrapping ErrUnsupportedDomain, so a
// hostile domain can never reach URL construction.
//
// The error names the allowlist and never echoes the rejected input, which would
// otherwise carry attacker-controlled text into a log line.
func ParseDomain(value string) (Domain, error) {
	candidate := Domain(strings.ToLower(strings.TrimSpace(value)))
	if candidate == "" {
		return DomainGlobal, nil
	}
	if !candidate.IsAllowed() {
		return "", errUnsupportedDomain()
	}
	return candidate, nil
}

func errUnsupportedDomain() error {
	names := make([]string, 0, len(allowedDomains))
	for _, allowed := range allowedDomains {
		names = append(names, string(allowed))
	}
	return fmt.Errorf("%w; allowed: %s", ErrUnsupportedDomain, strings.Join(names, ", "))
}

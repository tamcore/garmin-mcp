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
// (body, headers, service ticket, CSRF token, page title). Neither exposes it as
// a field — the fields are unexported and sit behind a pointer, so a reflective
// logger and a method-stripping alias both see an address — and both implement
// redacted String, GoString, MarshalJSON and slog.LogValuer, so printing,
// serializing or logging them never emits that material. Classification's
// accessors hand the real values to a caller that asks deliberately.
//
// Error renders only the package's own sanitized label constants plus a redacted
// cause. The cause rendering never uses a wrapper's own message text, because
// fmt.Errorf lets a caller put a bearer token or a cookie there; a recognized
// sentinel is rendered from the sentinel's own text, a *url.Error has its query
// redacted, and an unrecognized cause degrades to a network failure category or to
// its Go type name. Unwrap still exposes the real cause, so a caller that needs
// its text must fetch and redact it deliberately.
//
// Region safety: a Domain is only usable once ParseDomain or Domain.Validate has
// turned it into a ValidatedDomain. NewHosts fails closed on anything else rather
// than coercing it to DomainGlobal, which would send a China account's
// credentials to the global region.
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
// official Garmin domains are valid, and a Domain value alone carries no proof of
// that: a plain string conversion produces one. Turn caller-supplied input into a
// ValidatedDomain with ParseDomain, or an existing Domain with Domain.Validate,
// before it can reach URL construction.
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

// ValidatedDomain is a Domain proven to be on the allowlist. It carries that
// proof in the type, so a function taking one cannot be handed an
// attacker-supplied host: the wrapped field is unexported, which makes
// ParseDomain and Domain.Validate the only ways to obtain a populated value.
//
// The zero value holds no domain. It is not a region and does not default to one;
// see NewHostsForValidatedDomain for what it yields.
type ValidatedDomain struct {
	domain Domain
}

// Domain returns the validated region, or the zero Domain for a zero
// ValidatedDomain.
func (v ValidatedDomain) Domain() Domain { return v.domain }

// IsValid reports whether v holds an allowlisted domain. It is false only for the
// zero value.
func (v ValidatedDomain) IsValid() bool { return v.domain.IsAllowed() }

// String renders the validated region, or "" for the zero value.
func (v ValidatedDomain) String() string { return string(v.domain) }

// ParseDomain validates a caller-supplied region. It trims surrounding space and
// folds case; an empty string selects DomainGlobal, which is the documented
// default for an unspecified region. Any value outside the allowlist is rejected
// with an error wrapping ErrUnsupportedDomain, so a hostile domain can never
// reach URL construction.
//
// The error names the allowlist and never echoes the rejected input, which would
// otherwise carry attacker-controlled text into a log line.
func ParseDomain(value string) (ValidatedDomain, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ValidatedDomain{domain: DomainGlobal}, nil
	}
	return Domain(strings.ToLower(trimmed)).Validate()
}

// Validate proves that d is on the allowlist. Unlike ParseDomain it neither trims
// nor folds case, and it rejects the zero Domain: a Domain value that was never
// validated must not be read as a request for the default region.
func (d Domain) Validate() (ValidatedDomain, error) {
	if !d.IsAllowed() {
		return ValidatedDomain{}, errUnsupportedDomain()
	}
	return ValidatedDomain{domain: d}, nil
}

func errUnsupportedDomain() error {
	names := make([]string, 0, len(allowedDomains))
	for _, allowed := range allowedDomains {
		names = append(names, string(allowed))
	}
	return fmt.Errorf("%w; allowed: %s", ErrUnsupportedDomain, strings.Join(names, ", "))
}

package config

import (
	"net"
	"net/url"
	"strconv"
	"strings"
)

// This file holds the validation of the network-facing settings: where the
// Streamable HTTP listener binds, which canonical origin clients are told to
// use, how cleartext exposure is terminated, and which persistent state a remote
// deployment must have before it may start. Every check here is lexical; nothing
// binds, resolves, or opens anything.

// The only URL schemes a public origin may use.
const (
	schemeHTTP  = "http"
	schemeHTTPS = "https"
)

// validateHTTP checks the remote deployment surface: where the listener binds,
// what origin clients are told to use, whether that combination is protected,
// and whether the persistent state it needs is configured.
func (c Config) validateHTTP() []error {
	errs := c.validateBindAddress()
	errs = append(errs, c.validatePublicURL()...)
	errs = append(errs, c.validateTLSPair()...)
	errs = append(errs, c.validateProxyTrust()...)
	errs = append(errs, c.validateRemoteState()...)
	errs = append(errs, c.validateRegistry()...)
	errs = append(errs, c.validateAllowedOrigins()...)
	errs = append(errs, c.validateSessionTimeout()...)
	return errs
}

// validateBindAddress requires an explicit host:port and refuses an unprotected
// non-loopback listener.
func (c Config) validateBindAddress() []error {
	if c.BindAddress == "" {
		return []error{newFieldError(keyBindAddress,
			"is required for the streamable-http transport", ErrMissingSetting)}
	}

	host, err := splitListenHost(c.BindAddress)
	if err != nil {
		return []error{newFieldError(keyBindAddress,
			"must be host:port with a port between 1 and 65535", ErrInvalidConfig)}
	}
	if isLoopbackHost(host) || c.hasTransportProtection() {
		return nil
	}
	return []error{newFieldError(keyBindAddress,
		"binds a non-loopback address without TLS material, trusted proxy networks, "+
			"or the explicit insecure-http override", ErrInsecureSetting)}
}

// hasTransportProtection reports whether cleartext exposure is either terminated
// by this process, terminated by a trusted proxy, or explicitly accepted.
func (c Config) hasTransportProtection() bool {
	return c.TLSCertFile != "" || len(c.TrustedProxyCIDRs) > 0 || c.AllowInsecureHTTP
}

// validatePublicURL requires an explicit canonical origin. It must be absolute
// and free of userinfo, query, and fragment, because it is used to build the
// OAuth issuer, the resource identifier, and redirect targets.
func (c Config) validatePublicURL() []error {
	if c.PublicURL == "" {
		return []error{newFieldError(keyPublicURL,
			"is required for the streamable-http transport", ErrMissingSetting)}
	}

	parsed, err := url.Parse(c.PublicURL)
	switch {
	case err != nil, parsed.Host == "", parsed.Scheme != schemeHTTP && parsed.Scheme != schemeHTTPS:
		return []error{newFieldError(keyPublicURL,
			"must be an absolute http or https URL with a host", ErrInvalidConfig)}
	case parsed.User != nil, parsed.RawQuery != "", parsed.Fragment != "":
		return []error{newFieldError(keyPublicURL,
			"must not carry userinfo, a query, or a fragment", ErrInvalidConfig)}
	}

	if parsed.Scheme == schemeHTTP && !isLoopbackHost(parsed.Hostname()) && !c.AllowInsecureHTTP {
		return []error{newFieldError(keyPublicURL,
			"must use https for a non-loopback origin unless the insecure-http override is set",
			ErrInsecureSetting)}
	}
	return nil
}

// validateTLSPair requires a certificate and a key together: one alone cannot
// serve TLS, and starting anyway would silently fall back to cleartext.
func (c Config) validateTLSPair() []error {
	switch {
	case c.TLSCertFile != "" && c.TLSKeyFile == "":
		return []error{newFieldError(keyTLSKeyFile,
			"is required when a TLS certificate is configured", ErrMissingSetting)}
	case c.TLSKeyFile != "" && c.TLSCertFile == "":
		return []error{newFieldError(keyTLSCertFile,
			"is required when a TLS key is configured", ErrMissingSetting)}
	default:
		return nil
	}
}

// validateProxyTrust requires real CIDR notation. A bare address would silently
// trust nothing, or everything, depending on how it were later parsed.
func (c Config) validateProxyTrust() []error {
	for _, entry := range c.TrustedProxyCIDRs {
		if _, _, err := net.ParseCIDR(strings.TrimSpace(entry)); err != nil {
			return []error{newFieldError(keyTrustedProxyCIDRs,
				"must contain CIDR networks such as 10.0.0.0/8", ErrInvalidConfig)}
		}
	}
	return nil
}

// validateRemoteState requires the persistent state remote mode depends on and
// refuses inline secret material there. Inline material is an explicitly
// insecure compatibility override for local use only.
func (c Config) validateRemoteState() []error {
	var errs []error
	if c.DatabasePath == "" {
		errs = append(errs, newFieldError(keyDatabasePath,
			"is required for the streamable-http transport", ErrMissingSetting))
	}
	if c.MasterKeyPath == "" && !c.MasterKey.IsSet() {
		errs = append(errs, newFieldError(keyMasterKeyFile,
			"is required for the streamable-http transport", ErrMissingSetting))
	}
	if c.MasterKey.IsSet() {
		errs = append(errs, newFieldError(keyMasterKey,
			"must not be supplied inline for the streamable-http transport; use "+
				keyMasterKeyFile+" instead", ErrInsecureSetting))
	}
	if c.GarminTokens.IsSet() {
		errs = append(errs, newFieldError(keyGarminTokens,
			"must not be supplied inline for the streamable-http transport; use "+
				keyGarminTokensFile+" instead", ErrInsecureSetting))
	}
	return errs
}

// splitListenHost splits a host:port listen address and checks the port range. A
// zero port is rejected: an ephemeral listener cannot match a canonical public
// URL.
func splitListenHost(address string) (string, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", err
	}
	number, err := strconv.Atoi(port)
	if err != nil {
		return "", err
	}
	if number < 1 || number > 65535 {
		return "", strconv.ErrRange
	}
	return host, nil
}

// isLoopbackHost reports whether host is a loopback address or the loopback
// name. An empty host means every interface and is never loopback.
func isLoopbackHost(host string) bool {
	trimmed := strings.Trim(strings.TrimSpace(host), "[]")
	if trimmed == "" {
		return false
	}
	if strings.EqualFold(trimmed, "localhost") {
		return true
	}
	ip := net.ParseIP(trimmed)
	return ip != nil && ip.IsLoopback()
}

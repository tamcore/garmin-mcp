package protocol

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"syscall"
)

// Network failure categories. Each one names a class of transport failure and
// nothing else: no host, no address, no port, and no text from the underlying
// error, because every one of those is either attacker-influenced or reveals the
// deployment's network topology.
const (
	categoryDNSNotFound   = "dns name not found"
	categoryDNSTimeout    = "dns timeout"
	categoryDNSFailure    = "dns failure"
	categoryTLSCertVerify = "tls certificate verification failure"
	categoryTLSHandshake  = "tls handshake failure"
	categoryConnRefused   = "connection refused"
	categoryConnReset     = "connection reset"
	categoryUnreachable   = "network unreachable"
	categoryTimeout       = "network timeout"
	categoryNetFailure    = "network failure"
)

// networkCategory names the transport failure class behind err, so an operator
// can tell a DNS problem from a refused connection without the error text being
// rendered. The second result is false when err is not a recognized network
// shape.
//
// Callers must apply the context.Canceled and context.DeadlineExceeded checks
// first: context.DeadlineExceeded satisfies net.Error and reports a timeout, and
// naming the context cause is more useful than naming the transport.
func networkCategory(err error) (string, bool) {
	if category, ok := dnsCategory(err); ok {
		return category, true
	}
	if category, ok := tlsCategory(err); ok {
		return category, true
	}
	if category, ok := syscallCategory(err); ok {
		return category, true
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return categoryTimeout, true
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return categoryNetFailure, true
	}
	return "", false
}

func dnsCategory(err error) (string, bool) {
	var dnsErr *net.DNSError
	if !errors.As(err, &dnsErr) {
		return "", false
	}
	switch {
	case dnsErr.IsNotFound:
		return categoryDNSNotFound, true
	case dnsErr.IsTimeout:
		return categoryDNSTimeout, true
	default:
		return categoryDNSFailure, true
	}
}

// tlsCategory separates a certificate verification failure, which needs a trust
// store or clock fix, from any other handshake failure, which needs a look at
// the peer. Certificate errors name the rejected host, so none of their text is
// rendered.
func tlsCategory(err error) (string, bool) {
	var certErr *tls.CertificateVerificationError
	var unknownAuthority x509.UnknownAuthorityError
	var hostnameErr x509.HostnameError
	var invalidErr x509.CertificateInvalidError
	switch {
	case errors.As(err, &certErr), errors.As(err, &unknownAuthority),
		errors.As(err, &hostnameErr), errors.As(err, &invalidErr):
		return categoryTLSCertVerify, true
	}

	var recordErr tls.RecordHeaderError
	var alertErr tls.AlertError
	switch {
	case errors.As(err, &recordErr), errors.As(err, &alertErr):
		return categoryTLSHandshake, true
	}
	return "", false
}

func syscallCategory(err error) (string, bool) {
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return categoryConnRefused, true
	case errors.Is(err, syscall.ECONNRESET):
		return categoryConnReset, true
	case errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, syscall.ENETUNREACH):
		return categoryUnreachable, true
	default:
		return "", false
	}
}

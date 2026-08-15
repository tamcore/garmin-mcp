package cmd

import (
	"errors"
	"os"
	"strings"

	"github.com/tamcore/garmin-mcp/internal/config"
)

// checkRemote fills in the remote half of the report.
//
// Nothing here opens anything. The database is inspected by its mode alone,
// because opening it would migrate the very thing the operator asked to have
// looked at, and the client registry is read from configuration rather than from
// the registry builder, because building it would read every secret digest file
// for a report that must never hold one.
func (d *diagnosis) checkRemote(cfg config.Config) {
	d.PublicURL = cfg.PublicURL
	d.TLSConfigured = cfg.TLSCertFile != "" && cfg.TLSKeyFile != ""
	d.DatabasePath = cfg.DatabasePath
	d.ClientIDs = registeredClientIDs(cfg)
	d.checkDatabase(cfg)
}

// checkDatabase classifies the multi-user store from its mode.
//
// A database another local account can read holds every principal's encrypted
// Garmin tokens and every hashed MCP token record. The encryption key is a
// separate file, so a relaxed mode is not an immediate disclosure of tokens, but
// it is an operator mistake that must not be reported as a healthy deployment.
func (d *diagnosis) checkDatabase(cfg config.Config) {
	info, err := os.Stat(cfg.DatabasePath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		d.DatabaseState = stateAbsent
		d.DatabaseDetail = "absent; it is created and migrated on the first serve run"
	case err != nil:
		d.DatabaseState = stateUnsafe
		d.DatabaseDetail = "unreadable: " + sanitizedCause(err)
	case info.IsDir():
		d.DatabaseState = stateUnsafe
		d.DatabaseDetail = "present but is a directory"
	case info.Mode().Perm()&0o077 != 0:
		d.DatabaseState = stateUnsafe
		d.DatabaseDetail = detailReadable
	default:
		d.DatabaseState, d.DatabaseDetail = stateOK, detailOwnerOnly
	}
}

// registeredClientIDs reports the configured client identifiers.
//
// An identifier is operator-chosen configuration, and an operator diagnosing a
// refused client needs to see which registrations are in force. A redirect URI is
// not reported, because it names a third party, and a secret digest is not
// reported at all.
func registeredClientIDs(cfg config.Config) []string {
	if len(cfg.OAuthClients) == 0 {
		return nil
	}
	out := make([]string, 0, len(cfg.OAuthClients))
	for _, client := range cfg.OAuthClients {
		out = append(out, client.ID)
	}
	return out
}

// writeRemoteSection renders the remote half of the report.
func (d diagnosis) writeRemoteSection(b *strings.Builder) {
	b.WriteString("\nremote deployment:\n")
	writeLine(b, "  public url", d.PublicURL)
	writeLine(b, "  tls", tlsLabel(d.TLSConfigured))
	writeCheck(b, "  database", d.DatabasePath, d.DatabaseState, d.DatabaseDetail)
	writeLine(b, "  oauth clients", clientLabel(d.ClientIDs))
}

// tlsLabel states who terminates TLS. Neither answer is a fault: this process may
// hold the certificate, or a trusted proxy may.
func tlsLabel(configured bool) string {
	if configured {
		return "configured; this process terminates TLS"
	}
	return "not configured; a trusted proxy must terminate TLS"
}

// clientLabel renders the registered identifiers, or says there are none.
func clientLabel(ids []string) string {
	if len(ids) == 0 {
		return "none registered; no client can authorize"
	}
	return strings.Join(ids, ", ")
}

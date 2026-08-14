package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/tamcore/garmin-mcp/internal/config"
	"github.com/tamcore/garmin-mcp/internal/cryptostore"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/mcplog"
	"github.com/tamcore/garmin-mcp/internal/store"
	"github.com/tamcore/garmin-mcp/internal/tokenlink"
)

// Outbound HTTP bounds. They are separate from the whole-request timeout, so a
// stalled handshake fails fast while a slow but progressing response still
// completes inside the configured budget.
const (
	dialTimeout           = 10 * time.Second
	tlsHandshakeTimeout   = 10 * time.Second
	responseHeaderTimeout = 30 * time.Second
	idleConnTimeout       = 90 * time.Second
)

// The accepted log encodings, as config validated them.
const (
	logFormatJSON = "json"
	logFormatText = "text"
)

// newLoggers builds the two logging seams over one sink.
//
// The first is the MCP logger, whose closed event vocabulary is what keeps a body,
// a token, or a health metric out of a log line. The second is the plain
// *slog.Logger the Garmin packages accept; it is injected rather than left nil,
// because slog.Default is process-global state a test cannot capture and a caller
// cannot point away from standard error.
//
// Both refuse standard output: mcplog.New rejects it outright, and since both share
// the sink there is no configuration in which a log record joins the frame stream.
func newLoggers(cfg config.Config, sink io.Writer) (*mcplog.Logger, *slog.Logger, error) {
	level, err := parseLogLevel(cfg.LogLevel)
	if err != nil {
		return nil, nil, err
	}
	format, err := parseLogFormat(cfg.LogFormat)
	if err != nil {
		return nil, nil, err
	}

	logger, err := mcplog.New(sink, mcplog.Config{Level: level, Format: format})
	if err != nil {
		return nil, nil, fmt.Errorf("building the MCP logger: %w", err)
	}
	return logger, slog.New(newSlogHandler(sink, level, format)), nil
}

// parseLogLevel maps the validated level name onto an slog level.
func parseLogLevel(name string) (slog.Level, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(name)); err != nil {
		return 0, fmt.Errorf("%w: log level %q is not an slog level", config.ErrInvalidConfig, name)
	}
	return level, nil
}

// parseLogFormat maps the validated encoding name onto an mcplog format.
func parseLogFormat(name string) (mcplog.Format, error) {
	switch name {
	case logFormatJSON:
		return mcplog.FormatJSON, nil
	case logFormatText:
		return mcplog.FormatText, nil
	default:
		return 0, fmt.Errorf("%w: log format %q is not supported", config.ErrInvalidConfig, name)
	}
}

// newSlogHandler builds the handler the Garmin packages log through, matching the
// operator's chosen encoding so both streams read the same way.
func newSlogHandler(sink io.Writer, level slog.Level, format mcplog.Format) slog.Handler {
	opts := &slog.HandlerOptions{Level: level}
	if format == mcplog.FormatText {
		return slog.NewTextHandler(sink, opts)
	}
	return slog.NewJSONHandler(sink, opts)
}

// bindPrincipal binds the one account a local process serves.
//
// The resolver takes a context and nothing else, so no request and no tool argument
// can select an account. A configuration naming more than one account is refused
// rather than resolved by picking a winner; Config carries a single identifier
// today, so that case is unrepresentable and the refusal lives in identity.
func bindPrincipal(cfg config.Config) (identity.Principal, *identity.StdioResolver, error) {
	resolver, err := identity.NewStdioResolver(
		identity.StdioConfig{PrincipalIDs: []string{cfg.PrincipalID}})
	if err != nil {
		return identity.Principal{}, nil, fmt.Errorf("binding the local principal: %w", err)
	}

	principal, err := resolver.Resolve(context.Background())
	if err != nil {
		return identity.Principal{}, nil, fmt.Errorf("resolving the local principal: %w", err)
	}
	return principal, resolver, nil
}

// openTokenStore obtains the key material and opens the encrypted token store.
//
// Key material is read, or created once, through internal/cryptostore, which
// refuses material that is malformed, group- or world-readable, or reached through
// a symlink. Those refusals are start-up failures on purpose: a deployment that
// cannot protect its key must not serve.
func openTokenStore(cfg config.Config, paths statePaths) (*store.FileStore, *tokenlink.Store, error) {
	if cfg.MasterKey.IsSet() {
		return nil, nil, fmt.Errorf("%w: inline master key material is not supported; "+
			"supply the key through master-key-file", ErrUnsupportedKeyMaterial)
	}

	key, err := cryptostore.LoadOrCreateKey(paths.keys, keyVersion)
	if err != nil {
		return nil, nil, fmt.Errorf("opening the encryption key: %w", err)
	}

	files, err := store.NewFileStore(store.Config{
		Dir:                       paths.tokens,
		Key:                       key,
		AllowInsecureInlineTokens: cfg.GarminTokens.IsSet(),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("opening the token store: %w", err)
	}

	tokens, err := tokenlink.New(files)
	if err != nil {
		return nil, nil, fmt.Errorf("adapting the token store: %w", err)
	}
	return files, tokens, nil
}

// importConfiguredTokens seeds the store from a configured 0.3.x token document.
//
// An existing record is never replaced. That rule is the point: the stored record
// may already hold a refresh token Garmin rotated after the document was written,
// and overwriting it would lose the only usable credential.
func (d *dependencies) importConfiguredTokens(ctx context.Context) error {
	set, ok, err := d.configuredTokens()
	if err != nil || !ok {
		return err
	}

	principal := d.principal.ID()
	_, _, err = d.files.Load(ctx, principal)
	switch {
	case err == nil:
		return nil
	case !errors.Is(err, store.ErrNoTokens):
		return fmt.Errorf("reading the stored token set: %w", err)
	}

	if _, err := d.files.Save(ctx, principal, set, 0); err != nil {
		return fmt.Errorf("importing the configured token set: %w", err)
	}
	return nil
}

// configuredTokens reads the configured token document, if any. Inline JSON is an
// explicitly insecure compatibility override and the store refuses it unless the
// override is on; a mounted owner-only file is the supported form.
func (d *dependencies) configuredTokens() (store.TokenSet, bool, error) {
	switch {
	case d.cfg.GarminTokensPath != "":
		set, err := store.ImportLegacyTokenFile(d.cfg.GarminTokensPath)
		if err != nil {
			return store.TokenSet{}, false, fmt.Errorf("reading the configured token file: %w", err)
		}
		return set, true, nil
	case d.cfg.GarminTokens.IsSet():
		set, err := store.ParseInlineTokenJSON(d.cfg.GarminTokens.Reveal(), true)
		if err != nil {
			return store.TokenSet{}, false, fmt.Errorf("reading the inline token document: %w", err)
		}
		return set, true, nil
	default:
		return store.TokenSet{}, false, nil
	}
}

// newHTTPClient builds the outbound transport for Garmin.
//
// Redirects are never followed. A redirect the client obeys can carry a request
// with an Authorization header to a host the caller never chose, and the
// refresher's host allowlist inspects the request it was given rather than a later
// hop. Returning the response instead leaves that decision with the caller.
func newHTTPClient(cfg config.Config) *http.Client {
	return &http.Client{
		Timeout: cfg.RequestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: dialTimeout}).DialContext,
			TLSHandshakeTimeout:   tlsHandshakeTimeout,
			ResponseHeaderTimeout: responseHeaderTimeout,
			IdleConnTimeout:       idleConnTimeout,
			ForceAttemptHTTP2:     true,
		},
	}
}

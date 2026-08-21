package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/config"
	"github.com/tamcore/garmin-mcp/internal/garmin/auth"
	"github.com/tamcore/garmin-mcp/internal/oauthserver"
	"github.com/tamcore/garmin-mcp/internal/ratelimit"
	"github.com/tamcore/garmin-mcp/internal/store"
)

// TestTheOutboundClientNeverFollowsARedirect is a security property rather than a
// preference: a redirect this client obeyed could carry a request that already
// holds an Authorization header to a host nobody chose, and the refresher's host
// allowlist inspects the request it was handed rather than a later hop.
func TestTheOutboundClientNeverFollowsARedirect(t *testing.T) {
	t.Parallel()

	client := newHTTPClient(config.Config{RequestTimeout: 7 * time.Second})

	if client.CheckRedirect == nil {
		t.Fatal("the client follows redirects")
	}
	request, err := http.NewRequest(http.MethodGet, "https://example.invalid/", nil)
	if err != nil {
		t.Fatalf("building the probe request: %v", err)
	}
	if err := client.CheckRedirect(request, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Errorf("CheckRedirect = %v, want http.ErrUseLastResponse", err)
	}
	if client.Timeout != 7*time.Second {
		t.Errorf("Timeout = %v, want the configured budget", client.Timeout)
	}
}

// TestUnsupportedLogSettingsAreRefusedAsConfiguration keeps a log setting this
// build cannot honor a configuration fault, so an operator is told which setting
// to fix instead of being served a process that logs at a level nobody asked for.
func TestUnsupportedLogSettingsAreRefusedAsConfiguration(t *testing.T) {
	t.Parallel()

	if _, err := parseLogLevel("trace"); !errors.Is(err, config.ErrInvalidConfig) {
		t.Errorf("parseLogLevel(trace) = %v, want config.ErrInvalidConfig", err)
	}
	if _, err := parseLogFormat("logfmt"); !errors.Is(err, config.ErrInvalidConfig) {
		t.Errorf("parseLogFormat(logfmt) = %v, want config.ErrInvalidConfig", err)
	}
	if _, err := parseLogLevel("debug"); err != nil {
		t.Errorf("parseLogLevel(debug) = %v, want it accepted", err)
	}
	if _, err := parseLogFormat(logFormatText); err != nil {
		t.Errorf("parseLogFormat(text) = %v, want it accepted", err)
	}
}

// TestTheBurstNeverExceedsTheSustainedBudget keeps a lowered rate from silently
// keeping the shipped burst, which would let a caller exceed the budget the
// operator set for exactly one moment per refill.
func TestTheBurstNeverExceedsTheSustainedBudget(t *testing.T) {
	t.Parallel()

	if got := burstFor(3, ratelimit.DefaultReadBurst); got != 3 {
		t.Errorf("burstFor(3) = %d, want the lower sustained budget", got)
	}
	if got := burstFor(10_000, ratelimit.DefaultReadBurst); got != ratelimit.DefaultReadBurst {
		t.Errorf("burstFor(10000) = %d, want the shipped burst", got)
	}
}

// TestTheKeyDirectoryFollowsTheConfiguredKeyFile pins the rule that the key
// setting chooses a location and not a file name: internal/cryptostore owns the
// versioned name, so pointing at a file selects the directory it lives in.
func TestTheKeyDirectoryFollowsTheConfiguredKeyFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	keyFile := filepath.Join(root, "secrets", "master.key")

	paths, err := resolveStatePaths(config.Config{StateDir: root, MasterKeyPath: keyFile})
	if err != nil {
		t.Fatalf("resolveStatePaths = %v", err)
	}

	if paths.keys != filepath.Dir(keyFile) {
		t.Errorf("keys = %q, want the configured key file's directory", paths.keys)
	}
	if paths.keyFile(1) != filepath.Join(filepath.Dir(keyFile), "key-v1.json") {
		t.Errorf("keyFile = %q, want the versioned name cryptostore owns", paths.keyFile(1))
	}
	if paths.tokens != filepath.Join(root, tokensDirName) {
		t.Errorf("tokens = %q, want it under the state directory", paths.tokens)
	}
}

// TestTokenErrorsAreComparableInBothPackages keeps the storage sentinels usable by
// the consumer that never imports the store: the mapped error must match the
// consumer's sentinel and still match the storage one, because a caller that lost
// either would be reduced to comparing messages.
func TestTokenErrorsAreComparableInBothPackages(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		from error
		want error
	}{
		string(stateAbsent): {from: store.ErrNoTokens, want: auth.ErrNoTokens},
		"conflict":          {from: store.ErrVersionConflict, want: auth.ErrVersionConflict},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := translateTokenError(tc.from)
			if !errors.Is(got, tc.want) {
				t.Errorf("%v does not match the consumer sentinel %v", got, tc.want)
			}
			if !errors.Is(got, tc.from) {
				t.Errorf("%v no longer matches the storage sentinel %v", got, tc.from)
			}
		})
	}

	if translateTokenError(nil) != nil {
		t.Error("a nil error was translated into a failure")
	}
	other := errors.New("something else")
	if !errors.Is(translateTokenError(other), other) {
		t.Error("an unrecognized error was not passed through")
	}
}

// TestTokenSetConversionKeepsTheZeroValueZero keeps an absent record from becoming
// a set of empty credentials, which would then be presented to Garmin as if it
// were one.
func TestTokenSetConversionKeepsTheZeroValueZero(t *testing.T) {
	t.Parallel()

	if !authTokenSet(store.TokenSet{}).IsZero() {
		t.Error("an absent stored set became a non-zero credential set")
	}
	if !storeTokenSet(auth.TokenSet{}).IsZero() {
		t.Error("an absent credential set became a non-zero stored set")
	}

	expiry := time.Unix(0, 0).UTC()
	stored := store.NewTokenSet(
		"synthetic-di-token", "synthetic-refresh", "synthetic-client", expiry)

	round := storeTokenSet(authTokenSet(stored))
	if round.Token() != stored.Token() || round.RefreshToken() != stored.RefreshToken() {
		t.Error("the round trip did not preserve the token material")
	}
	if !round.ExpiresAt().Equal(expiry) {
		t.Errorf("ExpiresAt = %v, want %v", round.ExpiresAt(), expiry)
	}
}

// TestSanitizedCauseStaysOnOneLine keeps a multi-line cause from breaking the
// diagnostic report's one-line-per-check shape.
func TestSanitizedCauseStaysOnOneLine(t *testing.T) {
	t.Parallel()

	got := sanitizedCause(errors.New("first line\nsecond line"))

	if got != "first line second line" {
		t.Errorf("sanitizedCause = %q, want the cause on one line", got)
	}
}

// TestAPublicURLThatCannotCarryATokenIsRefused covers the issuer half of the
// cleartext rule. The authorization server names this URL as the issuer and mints
// tokens for it, so anything that would publish a bearer token in plaintext, hide
// a password in userinfo, or collide with a path this deployment serves itself is
// refused before a key is read or a database is opened.
func TestAPublicURLThatCannotCarryATokenIsRefused(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"cleartext":     "http://mcp.example.test/mcp",
		"no host":       "/mcp",
		"unparseable":   "https://mcp.example.test/mcp\x7f",
		"userinfo":      "https://user:secret@mcp.example.test/mcp",
		"reserved path": "https://mcp.example.test" + tokenPath,
	}
	for name, publicURL := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := newRemoteEndpoints(publicURL)
			if !errors.Is(err, ErrInsecureDeployment) {
				t.Fatalf("newRemoteEndpoints(%q) = %v, want ErrInsecureDeployment", publicURL, err)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Errorf("the refusal %q echoes the userinfo", err)
			}
		})
	}
}

// TestAComponentBuiltWithoutItsDependencyIsRefused keeps the wiring fail-closed: a
// component that accepted a nil dependency would be built, wired, and only fail at
// the first request, which is the worst moment to discover a wiring defect.
func TestAComponentBuiltWithoutItsDependencyIsRefused(t *testing.T) {
	t.Parallel()

	if _, err := newSQLiteTokens(nil); err == nil {
		t.Error("a token store was adapted from no database")
	}
	if _, err := newGrantedScopes(nil); err == nil {
		t.Error("a scope source was built without an authorizer")
	}
	if _, err := newAuthorizations(nil, nil); err == nil {
		t.Error("an authorization adapter was built without a server")
	}
}

// TestAClientRegistrationIsProjectedOntoTheServersShape covers the two
// projections a registration goes through. The display name falls back to the
// identifier because the consent screen must show something the operator chose,
// and a public client authenticates with "none" — which is only safe because PKCE
// S256 is mandatory on every authorization request.
func TestAClientRegistrationIsProjectedOntoTheServersShape(t *testing.T) {
	t.Parallel()

	unnamed := config.OAuthClient{ID: "example-client"}
	if got := clientDisplayName(unnamed); got != unnamed.ID {
		t.Errorf("clientDisplayName = %q, want the identifier as the fallback", got)
	}
	named := config.OAuthClient{ID: "example-client", Name: "Example MCP client"}
	if got := clientDisplayName(named); got != named.Name {
		t.Errorf("clientDisplayName = %q, want the configured name", got)
	}

	if got := authMethodOf(config.OAuthClient{Public: true}); got != "none" {
		t.Errorf("authMethodOf(public) = %q, want none", got)
	}
	if got := authMethodOf(config.OAuthClient{}); got == "none" {
		t.Error("a confidential client authenticates with none")
	}
}

// TestASecretDigestIsAcceptedInlineOrFromAFile keeps the secret plumbing's
// structural rules: a public client has no digest at all, an inline digest is a
// supported source for a confidential client, and a confidential client that
// registers none can authenticate nobody.
func TestASecretDigestIsAcceptedInlineOrFromAFile(t *testing.T) {
	t.Parallel()

	digest, err := clientDigest(config.OAuthClient{ID: "public-client", Public: true})
	if err != nil || digest != "" {
		t.Errorf("clientDigest(public) = %q, %v, want no digest and no error", digest, err)
	}

	const inlineDigest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcd"
	got, err := clientDigest(config.OAuthClient{
		ID:         "inline-client",
		SecretHash: config.NewSecret(inlineDigest),
	})
	if err != nil {
		t.Errorf("an inline digest = %v, want it accepted", err)
	}
	if got != inlineDigest {
		t.Errorf("clientDigest(inline) = %q, want %q", got, inlineDigest)
	}

	if _, err := clientDigest(config.OAuthClient{ID: "confidential-client"}); err == nil {
		t.Error("a confidential client with no digest was accepted")
	}
}

// sha256Hex returns the hex SHA-256 digest of secret, the same computation an
// operator runs offline to populate secret-hash or secret-hash-file.
func sha256Hex(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// TestAConfidentialClientAcceptsAnInlineDigest proves the inline digest is a
// supported way to register a confidential client: the built client
// authenticates the secret whose digest was supplied, and refuses any other.
func TestAConfidentialClientAcceptsAnInlineDigest(t *testing.T) {
	t.Parallel()

	const rawSecret = "correct-horse-battery-staple-0123456789"
	digest := sha256Hex(rawSecret)

	registration := confidentialRegistration()
	registration.SecretHash = config.NewSecret(digest)

	client, err := buildClient(registration)
	if err != nil {
		t.Fatalf("buildClient(inline digest) = %v, want it accepted", err)
	}
	if err := client.Authenticate(oauthserver.SecretFromString(rawSecret)); err != nil {
		t.Errorf("Authenticate(the registered secret) = %v, want nil", err)
	}
	if err := client.Authenticate(oauthserver.SecretFromString("wrong-secret")); err == nil {
		t.Error("Authenticate(a different secret) succeeded")
	}
}

// TestAPublicClientCarryingAnInlineDigestIsRefused keeps the structural rule
// that method "none" carries no digest, inline or by file.
func TestAPublicClientCarryingAnInlineDigestIsRefused(t *testing.T) {
	t.Parallel()

	registration := config.OAuthClient{ID: "public-client", Public: true,
		SecretHash: config.NewSecret(sha256Hex("whatever"))}

	if _, err := clientDigest(registration); err == nil {
		t.Error("a public client with an inline digest was accepted")
	}
}

// TestBothDigestSourcesSetIsRefused keeps a registration from silently picking
// one of two contradictory sources.
func TestBothDigestSourcesSetIsRefused(t *testing.T) {
	t.Parallel()

	registration := confidentialRegistration()
	registration.SecretHashPath = testDigestFilePath
	registration.SecretHash = config.NewSecret(sha256Hex("whatever"))

	if _, err := clientDigest(registration); err == nil {
		t.Error("a registration with both an inline digest and a digest file was accepted")
	}
}

// TestAnInlineDigestIsTrimmed keeps surrounding whitespace, which a
// newline-terminated environment variable commonly carries, from making
// ParseLookup's exact 64-hex-character check fail on an otherwise valid
// digest.
func TestAnInlineDigestIsTrimmed(t *testing.T) {
	t.Parallel()

	digest := sha256Hex("padded-secret")
	registration := confidentialRegistration()
	registration.SecretHash = config.NewSecret("  " + digest + "\n")

	got, err := clientDigest(registration)
	if err != nil {
		t.Fatalf("clientDigest(padded inline digest) = %v, want it accepted", err)
	}
	if got != digest {
		t.Errorf("clientDigest = %q, want the trimmed digest %q", got, digest)
	}
}

// confidentialRegistration is the smallest confidential registration that
// builds through [buildClient]; it carries no digest of its own.
func confidentialRegistration() config.OAuthClient {
	return config.OAuthClient{
		ID:           "confidential-client",
		RedirectURIs: []string{"https://client.example.test/callback"},
		Scopes:       []string{"garmin:read"},
		Resources:    []string{"https://mcp.example.test/mcp"},
	}
}

// testDigestFilePath is a placeholder path used only to prove the
// both-sources-set conflict; its content is never read in that case.
const testDigestFilePath = "/run/secrets/example-client.sha256"

// TestAStopTheOperatorAskedForIsNotAFailure keeps a clean shutdown out of the exit
// status a supervisor reads as a crash. A cancelled read surfaces as an ordinary
// I/O failure on some platforms, so the context is consulted as well as the error.
func TestAStopTheOperatorAskedForIsNotAFailure(t *testing.T) {
	t.Parallel()

	stopped, cancel := context.WithCancel(context.Background())
	cancel()
	running := context.Background()

	cases := map[string]struct {
		ctx  context.Context
		err  error
		want bool
	}{
		"clean end":         {ctx: running, err: nil, want: true},
		"cancelled":         {ctx: running, err: context.Canceled, want: true},
		"deadline":          {ctx: running, err: context.DeadlineExceeded, want: true},
		"read after cancel": {ctx: stopped, err: errors.New("file already closed"), want: true},
		"failure while serving": {ctx: running, err: errors.New("bind: address in use"),
			want: false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := isGracefulStop(tc.ctx, tc.err); got != tc.want {
				t.Errorf("isGracefulStop = %t, want %t", got, tc.want)
			}
		})
	}
}

// TestClosingAnUnbuiltGraphIsSafe keeps the deferred close usable from the moment
// a caller has a value, which is what stops an early failure from leaking the
// connections a later one opened.
func TestClosingAnUnbuiltGraphIsSafe(t *testing.T) {
	t.Parallel()

	var absent *dependencies
	absent.close()
	(&dependencies{}).close()
}

// TestNoStateDirectoryIsEverGuessed keeps an unresolvable layout a reported
// failure: guessing a location would split an operator's tokens across two
// directories, and only one of them would ever be read again.
func TestNoStateDirectoryIsEverGuessed(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("AppData", "")

	_, err := resolveStatePaths(config.Config{})

	if err == nil {
		t.Skip("this platform resolves a configuration directory without an environment")
	}
	if !errors.Is(err, ErrUnresolvedState) {
		t.Errorf("err = %v, want ErrUnresolvedState", err)
	}
}

// TestNilWritersSelectTheProcessStreams pins the documented default of Options: a
// caller that supplies nothing gets the process streams rather than a nil writer
// that would panic on the first diagnostic.
func TestNilWritersSelectTheProcessStreams(t *testing.T) {
	t.Parallel()

	opts := Options{}

	if opts.stdin() != os.Stdin {
		t.Error("a nil Stdin does not select os.Stdin")
	}
	if opts.stdout() != os.Stdout {
		t.Error("a nil Stdout does not select os.Stdout")
	}
	if opts.stderr() != os.Stderr {
		t.Error("a nil Stderr does not select os.Stderr")
	}
}

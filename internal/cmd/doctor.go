package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tamcore/garmin-mcp/internal/config"
	"github.com/tamcore/garmin-mcp/internal/cryptostore"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/store"
)

// A state is the outcome of one diagnostic check.
//
// The three values are distinct because an operator acts on them differently: a
// deployment that has not been set up yet is normal, and material another local
// account can read is not.
type state string

const (
	// stateOK reports a check that passed.
	stateOK state = "ok"
	// stateAbsent reports something that does not exist yet. It is informational:
	// a fresh deployment has no key, no store, and no linked account.
	stateAbsent state = "absent"
	// stateUnsafe reports something that exists and must not be used. It is the
	// only state that fails the command.
	stateUnsafe state = "unsafe"
)

// The two verdicts every mode check shares. They are constants because the key
// file, the token directory, and the database are judged by the same rule, and an
// operator grepping a report should find one wording rather than three.
const (
	detailOwnerOnly = "present, owner-only"
	detailReadable  = "present but not owner-only; another local account can read it"
)

// diagnosis is the complete doctor report.
//
// Every field is a string or a bool, and the effective configuration is carried
// pre-rendered through the redacted representation config already owns. That is
// deliberate: a report holding a config.Secret could leak it through a reflective
// logger or through fmt's badVerb path, so it holds none.
type diagnosis struct {
	// Transport is the configured MCP transport.
	Transport string
	// Region is the validated Garmin region.
	Region string
	// PrincipalID is the bound account identifier, and PrincipalBound reports
	// whether it is usable as one.
	PrincipalID    string
	PrincipalBound bool

	// StateDir, KeyFile and TokenDir are the resolved locations.
	StateDir string
	KeyFile  string
	TokenDir string

	// KeyState and KeyDetail describe the encryption key material.
	KeyState  state
	KeyDetail string
	// StoreState and StoreDetail describe the token store directory.
	StoreState  state
	StoreDetail string
	// TokensState and TokensDetail describe the bound principal's token record.
	TokensState  state
	TokensDetail string

	// Remote reports whether this is a Streamable HTTP deployment, which decides
	// which half of the report applies: a remote deployment has no single bound
	// account and no single-user token file, and a local one has no database, no
	// public URL, and no client registry.
	Remote bool
	// PublicURL is the canonical origin clients are told to use.
	PublicURL string
	// TLSConfigured reports whether this process terminates TLS itself. False
	// means a trusted proxy does, which is a supported shape.
	TLSConfigured bool
	// DatabasePath is the multi-user store, and DatabaseState and DatabaseDetail
	// describe what was found there.
	DatabasePath   string
	DatabaseState  state
	DatabaseDetail string
	// ClientIDs are the registered OAuth client identifiers. No other part of a
	// registration is reported, and a secret digest never is.
	ClientIDs []string

	// WriteEnabled and DestructiveEnabled are the operator half of the tier gate.
	WriteEnabled       bool
	DestructiveEnabled bool

	// ConfigLine is the effective configuration, already redacted.
	ConfigLine string
}

// failed reports whether any check found something that exists and must not be
// used. An absent key or an unlinked account is not a failure.
func (d diagnosis) failed() bool {
	return d.KeyState == stateUnsafe || d.StoreState == stateUnsafe ||
		d.TokensState == stateUnsafe || d.DatabaseState == stateUnsafe
}

// NewDoctorCommand reports deployment diagnostics: transport, principal binding,
// key material, token store, and the enabled tool tiers.
//
// The report goes to the result stream. That is not an exception to the MCP frame
// rule: the stdio server is started by serve, and doctor never starts one, so
// nothing on standard output can be mistaken for a frame.
func NewDoctorCommand(opts Options) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose the local deployment",
		Long: "Report the effective configuration and check the deployment.\n\n" +
			"Nothing is created and nothing secret is printed: the configuration is\n" +
			"rendered through its redacted representation.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}

			report, err := diagnose(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			if _, err := fmt.Fprint(opts.stdout(), report.render()); err != nil {
				return fmt.Errorf("writing the diagnostic report: %w", err)
			}
			if report.failed() {
				return ErrUnsafeDeployment
			}
			return nil
		},
	}
}

// diagnose runs every check against cfg without changing anything.
//
// Nothing here creates a directory, installs key material, or writes a record: a
// diagnostic that provisions what it is looking for cannot report that it was
// missing.
func diagnose(ctx context.Context, cfg config.Config) (diagnosis, error) {
	paths, err := resolveStatePaths(cfg)
	if err != nil {
		return diagnosis{}, err
	}

	report := diagnosis{
		Transport:          string(cfg.Transport),
		Region:             cfg.Region.String(),
		PrincipalID:        cfg.PrincipalID,
		PrincipalBound:     principalIsBound(cfg),
		StateDir:           paths.root,
		KeyFile:            paths.keyFile(),
		TokenDir:           paths.tokens,
		Remote:             cfg.Transport == config.TransportStreamableHTTP,
		WriteEnabled:       cfg.EnableWriteTools,
		DestructiveEnabled: cfg.EnableDestructiveTools,
		ConfigLine:         cfg.String(),
	}

	key, keyUsable := report.checkKey(cfg, paths)
	report.checkStore(paths)
	if report.Remote {
		report.checkRemote(cfg)
		return report, nil
	}
	report.checkTokens(ctx, cfg, paths, key, keyUsable)
	return report, nil
}

// principalIsBound reports whether the configured identifier is usable as a
// principal, using the same validation the server binds with.
func principalIsBound(cfg config.Config) bool {
	_, err := identity.NewPrincipal(cfg.PrincipalID)
	return err == nil
}

// checkKey reads the key material and classifies the outcome, returning the key
// when it is usable so the token check can open the store with it.
func (d *diagnosis) checkKey(cfg config.Config, paths statePaths) (cryptostore.Key, bool) {
	if cfg.MasterKey.IsSet() {
		d.KeyState = stateUnsafe
		d.KeyDetail = "inline master key material is not supported; " +
			"supply the key through master-key-file"
		return cryptostore.Key{}, false
	}

	key, err := cryptostore.LoadKey(paths.keys, keyVersion)
	switch {
	case err == nil:
		d.KeyState, d.KeyDetail = stateOK, detailOwnerOnly
		return key, true
	case errors.Is(err, cryptostore.ErrKeyNotFound):
		d.KeyState = stateAbsent
		d.KeyDetail = "absent; it is created on the first serve or auth run"
	case errors.Is(err, cryptostore.ErrInsecureKeyPermissions):
		d.KeyState = stateUnsafe
		d.KeyDetail = detailReadable
	default:
		d.KeyState = stateUnsafe
		d.KeyDetail = "present but unusable: " + sanitizedCause(err)
	}
	return cryptostore.Key{}, false
}

// checkStore classifies the token store directory from its mode alone, without
// opening it, because opening it would create it.
func (d *diagnosis) checkStore(paths statePaths) {
	info, err := os.Stat(paths.tokens)
	switch {
	case errors.Is(err, os.ErrNotExist):
		d.StoreState = stateAbsent
		d.StoreDetail = "absent; it is created on the first serve or auth run"
	case err != nil:
		d.StoreState = stateUnsafe
		d.StoreDetail = "unreadable: " + sanitizedCause(err)
	case !info.IsDir():
		d.StoreState = stateUnsafe
		d.StoreDetail = "present but is not a directory"
	case info.Mode().Perm()&0o077 != 0:
		d.StoreState = stateUnsafe
		d.StoreDetail = detailReadable
	default:
		d.StoreState, d.StoreDetail = stateOK, detailOwnerOnly
	}
}

// checkTokens reports whether the bound principal has a stored token set. It runs
// only when the key is usable and the store already exists, so the check cannot
// create either.
func (d *diagnosis) checkTokens(
	ctx context.Context, cfg config.Config, paths statePaths, key cryptostore.Key, keyUsable bool,
) {
	if !keyUsable || d.StoreState != stateOK || !d.PrincipalBound {
		d.TokensState = stateAbsent
		d.TokensDetail = "not checked; the key or the store is not in place yet"
		return
	}

	files, err := store.NewFileStore(store.Config{Dir: paths.tokens, Key: key})
	if err != nil {
		d.TokensState = stateUnsafe
		d.TokensDetail = "the token store cannot be opened: " + sanitizedCause(err)
		return
	}

	switch _, _, err := files.Load(ctx, cfg.PrincipalID); {
	case err == nil:
		d.TokensState = stateOK
		d.TokensDetail = "stored for the bound principal"
	case errors.Is(err, store.ErrNoTokens):
		d.TokensState = stateAbsent
		d.TokensDetail = "absent; run `garmin-mcp auth` to link the account"
	default:
		d.TokensState = stateUnsafe
		d.TokensDetail = "unreadable: " + sanitizedCause(err)
	}
}

// sanitizedCause renders a cause on one line. The packages this reports on already
// sanitize their messages and never quote file content.
func sanitizedCause(err error) string {
	return strings.ReplaceAll(err.Error(), "\n", " ")
}

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/tamcore/garmin-mcp/internal/config"
	"github.com/tamcore/garmin-mcp/internal/loginweb"
)

// The auth command's flags. None is a credential: an email address, a password, and
// a one-time code are absent by construction, because a command line is readable by
// every local process and lands in shell history.
const (
	flagNoBrowser = "no-browser"
	flagBrowser   = "browser"
	flagTTY       = "tty"
)

// Login failure sentinels.
var (
	// ErrLoginNotCompleted reports a run that ended without linking an account,
	// whether it was cancelled, expired, or refused by Garmin.
	ErrLoginNotCompleted = errors.New("the Garmin account was not linked")

	// ErrNoTerminal reports the terminal flow on a stream that is not a terminal.
	// It is a refusal rather than a fallback: reading a password from a pipe is
	// how a credential ends up in an MCP session, a log, or a CI transcript.
	ErrNoTerminal = errors.New("the terminal credential flow needs an attached terminal")

	// ErrConflictingFlow reports that both entry points were requested.
	ErrConflictingFlow = errors.New("the tty and browser flows are mutually exclusive")
)

// NewAuthCommand links a Garmin account to this deployment.
//
// The browser flow is the default: a one-shot listener on 127.0.0.1 with a
// kernel-chosen port serves the credential form, forwards the credentials to Garmin
// for one login, stores the resulting DI token set encrypted, and shuts down. The
// terminal flow is the explicitly requested alternative and needs an attached
// terminal.
//
// Credentials never arrive as arguments, as environment variables, or on MCP stdio.
func NewAuthCommand(opts Options) *cobra.Command {
	command := &cobra.Command{
		Use:   "auth",
		Short: "Link a Garmin account through a one-shot browser login",
		Long: "Link a Garmin account.\n\n" +
			"The browser flow serves a login form on 127.0.0.1 and shuts down after\n" +
			"success, cancellation, or expiry. The terminal flow reads the credentials\n" +
			"with echo disabled and needs an attached terminal.\n\n" +
			"Credentials are never accepted as flags, as environment variables, or over\n" +
			"MCP stdio.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			flow, err := selectFlow(cmd)
			if err != nil {
				return err
			}
			return runAuth(cmd.Context(), cfg, opts, flow)
		},
	}

	flags := command.Flags()
	flags.Bool(flagBrowser, true, "open the login page in the default browser")
	flags.Bool(flagNoBrowser, false, "print the login URL instead of opening a browser")
	flags.Bool(flagTTY, false, "read the credentials on the terminal instead of in a browser")
	return command
}

// loginFlow selects how the credentials are entered.
type loginFlow struct {
	// terminal selects the no-echo terminal prompt.
	terminal bool
	// openBrowser reports whether the browser flow should launch a browser.
	openBrowser bool
}

// selectFlow reads the flags and refuses a combination that has no meaning.
func selectFlow(cmd *cobra.Command) (loginFlow, error) {
	flags := cmd.Flags()
	terminal, _ := flags.GetBool(flagTTY)
	noBrowser, _ := flags.GetBool(flagNoBrowser)

	if terminal && (flags.Changed(flagBrowser) || flags.Changed(flagNoBrowser)) {
		return loginFlow{}, fmt.Errorf("%w: --%s cannot be combined with --%s or --%s",
			ErrConflictingFlow, flagTTY, flagBrowser, flagNoBrowser)
	}
	return loginFlow{terminal: terminal, openBrowser: !noBrowser}, nil
}

// runAuth assembles the dependency graph and runs the selected flow.
func runAuth(ctx context.Context, cfg config.Config, opts Options, flow loginFlow) error {
	deps, err := newDependencies(cfg, &wiring{
		Logs:    opts.stderr(),
		Version: opts.BuildInfo.Version,
	})
	if err != nil {
		return err
	}
	defer deps.close()

	if flow.terminal {
		return runTerminalLogin(ctx, deps, opts)
	}
	return runBrowserLogin(ctx, deps, opts, flow)
}

// runBrowserLogin serves the one-shot login pages on a loopback listener.
//
// The listener is bound before anything is printed or opened, so the URL an operator
// sees is the one that is actually accepting requests, and it is closed when the run
// ends whatever the outcome.
func runBrowserLogin(ctx context.Context, deps *dependencies, opts Options, flow loginFlow) error {
	server, err := loginweb.New(loginweb.Config{
		Authenticator: deps.loginSeam(),
		Logger:        deps.events,
	})
	if err != nil {
		return fmt.Errorf("building the login pages: %w", err)
	}

	listener, err := loginweb.ListenLoopback()
	if err != nil {
		return err
	}
	endpoint := loginweb.LoopbackURL(listener)

	announce(opts.stdout(), endpoint)
	if flow.openBrowser {
		if err := launchBrowser(ctx, endpoint); err != nil {
			deps.events.Warn("no browser could be opened; use the printed URL")
		}
	}

	if err := server.Serve(ctx, listener); err != nil {
		return err
	}
	return reportOutcome(opts.stdout(), server.Outcome())
}

// announce tells the operator where the login page is. It is this command's result,
// and auth never starts an MCP session, so the result stream is the right place.
func announce(w io.Writer, endpoint string) {
	_, _ = fmt.Fprintf(w, "Open this page to sign in to Garmin Connect:\n\n    %s\n\n"+
		"The page is served by this process on the loopback interface only, and it\n"+
		"stops as soon as the login finishes, is cancelled, or expires.\n", endpoint)
}

// reportOutcome renders the result and turns anything short of a linked account into
// a non-zero exit status.
func reportOutcome(w io.Writer, outcome loginweb.Outcome) error {
	if outcome.Succeeded() {
		_, _ = fmt.Fprintln(w, "The Garmin account is linked. The tokens are stored encrypted.")
		return nil
	}
	return fmt.Errorf("%w: the login run ended in state %q", ErrLoginNotCompleted, outcome.State)
}

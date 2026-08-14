package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/tamcore/garmin-mcp/internal/loginweb"
)

// The terminal flow's bounds. They mirror the browser form, so the same value is
// refused whichever way it is entered.
const (
	maxTerminalEmailLen    = loginweb.MaxEmailLen
	maxTerminalPasswordLen = loginweb.MaxPasswordLen
	maxTerminalCodeLen     = loginweb.MaxCodeLen
)

// ErrCredentialTooLong reports a value over the bound the login accepts. The value
// is never echoed back.
var ErrCredentialTooLong = errors.New("a credential is longer than the login accepts")

// runTerminalLogin reads the credentials on the terminal and runs one login.
//
// The terminal device is opened directly rather than read from standard input. That
// is the rule that keeps Garmin credentials off MCP stdio: when this process serves
// MCP, standard input carries protocol frames, and a prompt reading from it would
// consume one — or be answered by a model's output.
func runTerminalLogin(ctx context.Context, deps *dependencies, opts Options) error {
	tty, err := openTerminal()
	if err != nil {
		return err
	}
	defer func() { _ = tty.Close() }()

	prompt := opts.stderr()
	seam := deps.loginSeam()

	email, password, err := readCredentials(tty, prompt)
	if err != nil {
		return err
	}

	attempt, err := seam.Login(ctx, email, password)
	dropCredentials(&email, &password)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrLoginNotCompleted, err)
	}

	if attempt.NeedsMFA {
		if err := completeTerminalMFA(ctx, seam, tty, prompt, attempt); err != nil {
			return err
		}
	}

	_, _ = fmt.Fprintln(opts.stdout(),
		"The Garmin account is linked. The tokens are stored encrypted.")
	return nil
}

// completeTerminalMFA prompts for the one-time code and submits it.
func completeTerminalMFA(
	ctx context.Context, seam loginSeam, tty *os.File, prompt io.Writer, challenge loginweb.Attempt,
) error {
	describeChallenge(prompt, challenge)

	code, err := readSecret(tty, prompt, "One-time code: ", maxTerminalCodeLen)
	if err != nil {
		return err
	}

	_, err = seam.CompleteMFA(ctx, challenge.TransactionID, code)
	dropCredentials(&code)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrLoginNotCompleted, err)
	}
	return nil
}

// describeChallenge tells the user what Garmin asked for, without repeating anything
// they typed.
func describeChallenge(w io.Writer, challenge loginweb.Attempt) {
	if challenge.MFAMethod != "" {
		_, _ = fmt.Fprintf(w, "Garmin sent a one-time code by %s.\n", challenge.MFAMethod)
	} else {
		_, _ = fmt.Fprintln(w, "Garmin asked for a one-time code.")
	}
	if challenge.DeliveryUncertain {
		_, _ = fmt.Fprintln(w, "Delivery could not be confirmed. If no code arrives, start again.")
	}
}

// readCredentials prompts for the account and the password, with echo disabled for
// the password.
func readCredentials(tty *os.File, prompt io.Writer) (email, password string, err error) {
	_, _ = fmt.Fprintln(prompt,
		"These credentials are used for one Garmin login and are not stored.")

	email, err = readLine(tty, prompt, "Garmin email address: ", maxTerminalEmailLen)
	if err != nil {
		return "", "", err
	}
	password, err = readSecret(tty, prompt, "Garmin password: ", maxTerminalPasswordLen)
	if err != nil {
		return "", "", err
	}
	return email, password, nil
}

// readLine prompts and reads one echoed line, bounded before it is used.
func readLine(tty *os.File, prompt io.Writer, label string, maxLen int) (string, error) {
	_, _ = fmt.Fprint(prompt, label)

	reader := bufio.NewReaderSize(io.LimitReader(tty, int64(maxLen)+2), maxLen+2)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("reading from the terminal: %w", err)
	}
	return boundedValue(strings.TrimRight(line, "\r\n"), maxLen)
}

// readSecret prompts and reads one line with echo disabled, so the value never
// appears on the screen, in a scrollback buffer, or in a screen recording.
func readSecret(tty *os.File, prompt io.Writer, label string, maxLen int) (string, error) {
	_, _ = fmt.Fprint(prompt, label)

	value, err := readWithoutEcho(tty, maxLen)
	_, _ = fmt.Fprintln(prompt)
	if err != nil {
		return "", err
	}
	return boundedValue(value, maxLen)
}

// dropCredentials clears the variables that held credential material as soon as the
// Garmin call returns, so no later code path can reach them.
//
// It takes pointers because that is what makes the clearing observable: a plain
// assignment to a variable that is never read again is dead code a compiler and a
// linter may both discard. Go promises nothing about erasing the underlying string
// from memory, and this does not claim otherwise.
func dropCredentials(values ...*string) {
	for _, value := range values {
		*value = ""
	}
}

// boundedValue enforces a field bound without echoing the value.
func boundedValue(value string, maxLen int) (string, error) {
	if len(value) > maxLen {
		return "", ErrCredentialTooLong
	}
	return value, nil
}

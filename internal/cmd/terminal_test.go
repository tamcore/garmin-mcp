package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
	"github.com/tamcore/garmin-mcp/internal/loginweb"
)

// The synthetic credentials the terminal tests type. Neither names a real account.
const (
	testTerminalEmail    = "tester@example.invalid"
	testTerminalPassword = "not-a-real-password"
	testTerminalCode     = "424242"

	// testMFAMethod is the delivery method Garmin names in a challenge.
	testMFAMethod = "email"

	// testMFATransactionID is the synthetic Garmin continuation capability the
	// terminal MFA tests use in place of a real one.
	testMFATransactionID = "synthetic-transaction"
)

// fakeTerminal returns a file the prompts read from, preloaded with typed.
//
// A pipe is not a terminal, which is the point: the echoed prompt reads from it
// exactly as it would from a device, and the echo-disabled prompt refuses, because
// echo cannot be turned off on something that has no terminal state. Both are real
// behaviors of the flow rather than stand-ins for it.
func fakeTerminal(t *testing.T, typed string) *os.File {
	t.Helper()

	read, write, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating the fake terminal: %v", err)
	}
	t.Cleanup(func() { _ = read.Close() })

	go func() {
		defer func() { _ = write.Close() }()
		_, _ = write.WriteString(typed)
	}()
	return read
}

// TestReadLineReadsOneBoundedLine covers the echoed prompt: the label reaches the
// operator, and the answer comes back without its line ending.
func TestReadLineReadsOneBoundedLine(t *testing.T) {
	var prompt bytes.Buffer

	value, err := readLine(fakeTerminal(t, testTerminalEmail+"\r\n"),
		&prompt, "Garmin email address: ", maxTerminalEmailLen)
	if err != nil {
		t.Fatalf("readLine = %v, want the typed line", err)
	}

	if value != testTerminalEmail {
		t.Errorf("value = %q, want the typed address without its line ending", value)
	}
	if !strings.Contains(prompt.String(), "Garmin email address") {
		t.Errorf("prompt = %q, want the label", prompt.String())
	}
}

// TestReadLineRefusesAValueOverTheBoundWithoutEchoingIt keeps an oversized entry
// from becoming a login attempt, and keeps the refusal from repeating what was
// typed.
func TestReadLineRefusesAValueOverTheBoundWithoutEchoingIt(t *testing.T) {
	var prompt bytes.Buffer
	oversized := strings.Repeat("1", maxTerminalCodeLen+1)

	_, err := readLine(fakeTerminal(t, oversized+"\n"),
		&prompt, "One-time code: ", maxTerminalCodeLen)

	if !errors.Is(err, ErrCredentialTooLong) {
		t.Fatalf("err = %v, want ErrCredentialTooLong", err)
	}
	if strings.Contains(err.Error(), oversized) {
		t.Error("the refusal echoes the value that was typed")
	}
}

// TestReadSecretRefusesAStreamItCannotSilence is the rule that keeps a password
// off the screen: without a terminal whose echo can be turned off, the prompt is
// refused rather than served with echo left on.
func TestReadSecretRefusesAStreamItCannotSilence(t *testing.T) {
	var prompt bytes.Buffer

	_, err := readSecret(fakeTerminal(t, testTerminalPassword+"\n"),
		&prompt, "Garmin password: ", maxTerminalPasswordLen)

	if !errors.Is(err, ErrNoTerminal) {
		t.Fatalf("err = %v, want ErrNoTerminal", err)
	}
	if strings.Contains(err.Error(), testTerminalPassword) {
		t.Error("the refusal carries the password")
	}
}

// TestReadCredentialsPromptsForBothAndSaysTheyAreNotStored covers the pair: the
// account is read first, the password is read with echo disabled, and the operator
// is told what happens to the credentials before typing either.
func TestReadCredentialsPromptsForBothAndSaysTheyAreNotStored(t *testing.T) {
	var prompt bytes.Buffer

	_, password, err := readCredentials(
		fakeTerminal(t, testTerminalEmail+"\n"+testTerminalPassword+"\n"), &prompt)

	if !errors.Is(err, ErrNoTerminal) {
		t.Fatalf("err = %v, want the password prompt to refuse a non-terminal", err)
	}
	if password != "" {
		t.Error("a refused prompt still returned a password")
	}
	if !strings.Contains(prompt.String(), "not stored") {
		t.Errorf("prompt = %q, want it to say the credentials are not stored", prompt.String())
	}
	if strings.Contains(prompt.String(), testTerminalPassword) {
		t.Error("the prompt stream carries the password")
	}
}

// TestCompleteTerminalMFADescribesTheChallengeBeforeAsking keeps the operator able
// to tell which code to wait for, and stops before submitting anything it could
// not read securely.
func TestCompleteTerminalMFADescribesTheChallengeBeforeAsking(t *testing.T) {
	var prompt bytes.Buffer
	challenge := loginweb.Attempt{
		NeedsMFA: true, TransactionID: testMFATransactionID,
		MFAMethod: testMFAMethod, DeliveryUncertain: true,
	}

	err := completeTerminalMFA(context.Background(), loginSeam{},
		fakeTerminal(t, testTerminalCode+"\n"), &prompt, challenge)

	if !errors.Is(err, ErrNoTerminal) {
		t.Fatalf("err = %v, want the code prompt to refuse a non-terminal", err)
	}
	for _, want := range []string{testMFAMethod, "Delivery could not be confirmed"} {
		if !strings.Contains(prompt.String(), want) {
			t.Errorf("prompt = %q, want it to mention %q", prompt.String(), want)
		}
	}
	if strings.Contains(prompt.String(), challenge.TransactionID) {
		t.Error("the prompt stream carries the transaction capability")
	}
}

// fakeMFASeam scripts CompleteMFA one call at a time, so a test can prove the
// terminal loop asks again after a rejected code and stops after a terminal one.
type fakeMFASeam struct {
	responses []mfaSeamResponse
	calls     int
}

type mfaSeamResponse struct {
	attempt loginweb.Attempt
	err     error
}

func (f *fakeMFASeam) Login(context.Context, string, string) (loginweb.Attempt, error) {
	return loginweb.Attempt{}, errors.New("fakeMFASeam.Login is not used by these tests")
}

func (f *fakeMFASeam) CompleteMFA(context.Context, string, string) (loginweb.Attempt, error) {
	if f.calls >= len(f.responses) {
		return loginweb.Attempt{}, errors.New("fakeMFASeam: no more scripted responses")
	}
	next := f.responses[f.calls]
	f.calls++
	return next.attempt, next.err
}

// scriptedCodes returns a readCode function that hands out codes in order and
// fails if asked for more than were scripted.
func scriptedCodes(codes ...string) func() (string, error) {
	next := 0
	return func() (string, error) {
		if next >= len(codes) {
			return "", errors.New("scriptedCodes: no more codes")
		}
		code := codes[next]
		next++
		return code, nil
	}
}

// TestCompleteMFALoopRetriesARejectedCodeButNotATerminalFailure covers the item-5
// fix for the CLI: a rejected code must prompt again on the same transaction,
// while a terminal failure — an account lockout here — must abort the login
// instead of asking for another code Garmin cannot accept anyway. The code
// prompt is scripted directly, because a real terminal (needed by readSecret's
// echo-disabled read) cannot be simulated with a pipe in this test process.
func TestCompleteMFALoopRetriesARejectedCodeButNotATerminalFailure(t *testing.T) {
	t.Run("retries a rejected code then succeeds", func(t *testing.T) {
		var prompt bytes.Buffer
		seam := &fakeMFASeam{responses: []mfaSeamResponse{
			{err: protocol.ErrMFARejected},
			{attempt: loginweb.Attempt{}},
		}}
		challenge := loginweb.Attempt{NeedsMFA: true, TransactionID: testMFATransactionID}

		err := completeMFALoop(context.Background(), seam, &prompt, challenge,
			scriptedCodes(testTerminalCode, testTerminalCode))
		if err != nil {
			t.Fatalf("completeMFALoop = %v, want the retry to succeed", err)
		}
		if seam.calls != 2 {
			t.Errorf("CompleteMFA was called %d times, want 2", seam.calls)
		}
		if !strings.Contains(prompt.String(), "not accepted") {
			t.Errorf("prompt = %q, want it to say the code was not accepted", prompt.String())
		}
	})

	t.Run("aborts on a terminal failure without asking again", func(t *testing.T) {
		var prompt bytes.Buffer
		seam := &fakeMFASeam{responses: []mfaSeamResponse{
			{err: protocol.ErrAccountLocked},
		}}
		challenge := loginweb.Attempt{NeedsMFA: true, TransactionID: testMFATransactionID}

		err := completeMFALoop(context.Background(), seam, &prompt, challenge,
			scriptedCodes(testTerminalCode))
		if !errors.Is(err, ErrLoginNotCompleted) {
			t.Fatalf("err = %v, want ErrLoginNotCompleted", err)
		}
		if !errors.Is(err, protocol.ErrAccountLocked) {
			t.Errorf("err = %v, want it to still carry protocol.ErrAccountLocked", err)
		}
		if seam.calls != 1 {
			t.Errorf("CompleteMFA was called %d times, want exactly 1: a terminal "+
				"failure must not prompt for another code", seam.calls)
		}
	})
}

// TestDescribeChallengeStaysUsefulWithoutAMethod covers the case where Garmin
// names no delivery method: the operator still learns that a code is expected.
func TestDescribeChallengeStaysUsefulWithoutAMethod(t *testing.T) {
	var prompt bytes.Buffer

	describeChallenge(&prompt, loginweb.Attempt{NeedsMFA: true})

	if !strings.Contains(prompt.String(), "one-time code") {
		t.Errorf("prompt = %q, want it to ask for a one-time code", prompt.String())
	}
}

// TestDropCredentialsClearsEveryValue pins the clearing the login path relies on.
func TestDropCredentialsClearsEveryValue(t *testing.T) {
	email, password := testTerminalEmail, testTerminalPassword

	dropCredentials(&email, &password)

	if email != "" || password != "" {
		t.Errorf("email = %q and password = %q, want both cleared", email, password)
	}
}

// TestBrowserCommandIsCorrectOnEveryPlatform covers each launcher, including the
// platform that has none. The helper is fixed per platform and never composed from
// a caller's string, so the endpoint must arrive as exactly one argument.
func TestBrowserCommandIsCorrectOnEveryPlatform(t *testing.T) {
	t.Parallel()

	const endpoint = "http://127.0.0.1:8123/login"
	cases := []struct {
		goos string
		name string
		args []string
	}{
		{goos: osDarwin, name: "open", args: []string{endpoint}},
		{goos: osLinux, name: xdgOpen, args: []string{endpoint}},
		{goos: "openbsd", name: xdgOpen, args: []string{endpoint}},
		{
			goos: osWindows, name: "rundll32",
			args: []string{"url.dll,FileProtocolHandler", endpoint},
		},
		{goos: "plan9"},
	}

	for _, tc := range cases {
		t.Run(tc.goos, func(t *testing.T) {
			t.Parallel()

			gotName, gotArgs := browserCommand(tc.goos, endpoint)
			if gotName != tc.name {
				t.Errorf("launcher = %q, want %q", gotName, tc.name)
			}
			if len(gotArgs) != len(tc.args) {
				t.Fatalf("arguments = %v, want %v", gotArgs, tc.args)
			}
			for index, want := range tc.args {
				if gotArgs[index] != want {
					t.Errorf("argument %d = %q, want %q", index, gotArgs[index], want)
				}
			}
		})
	}
}

// TestLaunchBrowserOpensOnlyThisRunsLoopbackPage keeps the launcher from becoming
// a way to open an arbitrary URL: anything that is not this process's own loopback
// page is refused before a helper is started.
func TestLaunchBrowserOpensOnlyThisRunsLoopbackPage(t *testing.T) {
	t.Parallel()

	for _, endpoint := range []string{
		"https://example.invalid/login",
		"file:///etc/passwd",
		"http://127.0.0.1.example.invalid/login",
		"",
	} {
		t.Run(endpoint, func(t *testing.T) {
			t.Parallel()

			err := launchBrowser(context.Background(), endpoint)
			if !errors.Is(err, ErrNoBrowser) {
				t.Errorf("launchBrowser(%q) = %v, want ErrNoBrowser", endpoint, err)
			}
		})
	}
}

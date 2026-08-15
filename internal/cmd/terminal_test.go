package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/loginweb"
)

// The synthetic credentials the terminal tests type. Neither names a real account.
const (
	testTerminalEmail    = "tester@example.invalid"
	testTerminalPassword = "not-a-real-password"
	testTerminalCode     = "424242"

	// testMFAMethod is the delivery method Garmin names in a challenge.
	testMFAMethod = "email"
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
		NeedsMFA: true, TransactionID: "synthetic-transaction",
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

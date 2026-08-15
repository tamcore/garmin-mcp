package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot is the module root relative to this package's directory, which is
// where `go test` runs.
const repoRoot = "../.."

// TestRunWritesTheNoticesToAFile covers the default path: the command is run
// from the repository root with no flags and overwrites THIRD_PARTY_NOTICES.md
// in place. That in-place default is easy to invoke by accident, so this test
// writes to a temporary path instead and compares against the checked-in file,
// which proves the two agree without putting the real one at risk.
func TestRunWritesTheNoticesToAFile(t *testing.T) {
	requireResolvableModuleGraph(t)

	out := filepath.Join(t.TempDir(), "NOTICES.md")

	if err := run(context.Background(), repoRoot, out); err != nil {
		t.Fatalf("run: %v", err)
	}

	written, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading the generated file: %v", err)
	}

	checkedIn, err := os.ReadFile(filepath.Join(repoRoot, noticesFile))
	if err != nil {
		t.Fatalf("reading %s: %v", noticesFile, err)
	}

	if !bytes.Equal(written, checkedIn) {
		t.Errorf("the generated file differs from the checked-in %s; "+
			"internal/notices carries the byte-level comparison", noticesFile)
	}

	if len(written) == 0 {
		t.Error("the generated file is empty")
	}
}

// TestRunWritesToStdoutOnDash pins the `-o -` escape hatch. It exists so the
// output can be diffed without touching the working tree, and a regression that
// silently wrote a file named "-" instead would be invisible in normal use.
//
// This test is not parallel, because it swaps a process-wide global.
func TestRunWritesToStdoutOnDash(t *testing.T) {
	requireResolvableModuleGraph(t)

	original := os.Stdout
	t.Cleanup(func() { os.Stdout = original })

	read, write, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}

	os.Stdout = write

	// Drained on a goroutine: the notices are far larger than the pipe buffer,
	// so a write that nobody reads would block forever.
	captured := make(chan []byte, 1)

	go func() {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(read)
		captured <- buf.Bytes()
	}()

	runErr := run(context.Background(), repoRoot, "-")
	_ = write.Close()
	os.Stdout = original

	if runErr != nil {
		t.Fatalf("run: %v", runErr)
	}

	got := <-captured
	if !bytes.HasPrefix(got, []byte("# Third-party notices")) {
		t.Errorf("stdout does not start with the notices heading; first bytes: %.40q", got)
	}

	if _, err := os.Stat("-"); err == nil {
		t.Error(`run created a file literally named "-" instead of writing to stdout`)
	}
}

// TestRunFailsWhenTheModuleGraphCannotBeResolved pins the fail-loud contract at
// the command boundary. A licence set that cannot be established must stop the
// run: emitting a partial or placeholder notices file would ship a distribution
// condition the project cannot honour.
func TestRunFailsWhenTheModuleGraphCannotBeResolved(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "NOTICES.md")

	err := run(context.Background(), dir, out)
	if err == nil {
		t.Fatal("run succeeded outside a module; want an error")
	}

	if !strings.Contains(err.Error(), "notices:") {
		t.Errorf("error %q does not name the operation", err)
	}

	if _, statErr := os.Stat(out); statErr == nil {
		t.Error("run wrote an output file despite failing")
	}
}

// TestExecuteReportsTheExitStatus covers the argument layer: the status main
// hands to os.Exit. A generator that failed but reported success would let a
// dependency bump land with stale notices and a green pipeline.
func TestExecuteReportsTheExitStatus(t *testing.T) {
	requireResolvableModuleGraph(t)

	dir := t.TempDir()

	cases := []struct {
		name string
		args []string
		want int
	}{
		{
			name: "generated",
			args: []string{"-C", repoRoot, "-o", filepath.Join(dir, "ok.md")},
			want: 0,
		},
		{
			name: "generation failed",
			args: []string{"-C", dir, "-o", filepath.Join(dir, "fail.md")},
			want: 1,
		},
		{
			name: "unknown flag",
			args: []string{"-nope"},
			want: 2,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer

			if got := execute(context.Background(), tc.args, &stderr); got != tc.want {
				t.Errorf("execute(%v) = %d, want %d (stderr: %s)",
					tc.args, got, tc.want, strings.TrimSpace(stderr.String()))
			}

			if tc.want != 0 && stderr.Len() == 0 {
				t.Error("a failing run said nothing on stderr")
			}
		})
	}

	// The unknown-flag case must not have defaulted to overwriting the real
	// notices file in the working directory.
	if _, err := os.Stat(noticesFile); err == nil {
		t.Errorf("a failed argument parse wrote %s into the package directory", noticesFile)
	}
}

// requireResolvableModuleGraph skips when the module graph cannot be resolved at
// all, which is the one condition under which these tests prove nothing rather
// than failing honestly.
func requireResolvableModuleGraph(t *testing.T) {
	t.Helper()

	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain in PATH: cannot resolve the module graph")
	}

	cmd := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", "./cmd/garmin-mcp")
	cmd.Dir = repoRoot

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("go list cannot resolve the released binary, so the generator cannot run here "+
			"(run `go mod download` with network access): %v: %s",
			err, strings.TrimSpace(string(out)))
	}
}

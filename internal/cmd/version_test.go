package cmd_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/cmd"
)

// TestVersionPrintsInjectedBuildInfo is the behavior that forces the Cobra tree
// into existence: `garmin-mcp version` must report exactly the version and
// commit that cmd/garmin-mcp/main.go receives from the release ldflags.
func TestVersionPrintsInjectedBuildInfo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		info cmd.BuildInfo
		want []string
	}{
		{
			name: "development defaults",
			info: cmd.BuildInfo{Version: "dev", Commit: "none"},
			want: []string{"dev", "none"},
		},
		{
			name: "release ldflags",
			info: cmd.BuildInfo{Version: "v1.2.3", Commit: "414b5402"},
			want: []string{"v1.2.3", "414b5402"},
		},
		{
			name: "empty build info reports unknown",
			info: cmd.BuildInfo{},
			want: []string{"unknown"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer
			code := cmd.Execute(context.Background(), cmd.Options{
				BuildInfo: tc.info,
				Args:      []string{cmdVersion},
				Stdout:    &stdout,
				Stderr:    &stderr,
			})

			if code != 0 {
				t.Fatalf("exit code = %d, want 0; stderr=%q", code, stderr.String())
			}
			for _, want := range tc.want {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("stdout %q does not contain %q", stdout.String(), want)
				}
			}
		})
	}
}

// TestVersionWritesNothingToStderr keeps `version` usable in scripts.
func TestVersionWritesNothingToStderr(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	code := cmd.Execute(context.Background(), cmd.Options{
		BuildInfo: cmd.BuildInfo{Version: "v0.0.1", Commit: "abcdef1"},
		Args:      []string{cmdVersion},
		Stdout:    &stdout,
		Stderr:    &stderr,
	})

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

package config

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

// malformedWithSecret is a configuration file whose broken line carries inline
// master key material: a scalar tagged as an integer. The YAML parser quotes the
// offending scalar — "cannot decode !!str `...` as a !!int" — so the parser error
// itself holds the master key.
const malformedWithSecret = "master-key: !!int \"" + sentinelSecret + "\"\n"

// errorRenderings collects the ways an error reaches an operator log.
func errorRenderings(err error) map[string]string {
	return map[string]string{
		"Error()":    err.Error(),
		"%v":         fmt.Sprintf("%v", err),
		"%+v":        fmt.Sprintf("%+v", err),
		verbGoSyntax: fmt.Sprintf(verbGoSyntax, err),
		"chain":      strings.Join(chainTexts(err), " | "),
	}
}

// maxChainDepth bounds the walk over a possibly self-referential error tree.
const maxChainDepth = 16

// chainTexts renders every error reachable from err, which is what an
// error-chain walker or a %+v-style logger has access to.
func chainTexts(err error) []string {
	return chainTextsAt(err, maxChainDepth)
}

func chainTextsAt(err error, depth int) []string {
	if err == nil || depth <= 0 {
		return nil
	}
	out := []string{err.Error(), fmt.Sprintf(verbGoSyntax, err)}
	switch unwrapped := err.(type) {
	case interface{ Unwrap() error }:
		out = append(out, chainTextsAt(unwrapped.Unwrap(), depth-1)...)
	case interface{ Unwrap() []error }:
		for _, inner := range unwrapped.Unwrap() {
			out = append(out, chainTextsAt(inner, depth-1)...)
		}
	}
	return out
}

// TestConfigFileErrorNeverRendersTheParserCause is the MEDIUM finding: the raw
// parser error quotes the malformed line, which may hold an inline secret, so it
// must not be reachable through the public error chain.
func TestConfigFileErrorNeverRendersTheParserCause(t *testing.T) {
	file := writeConfigFile(t, malformedWithSecret)

	_, err := Load(LoadOptions{ConfigFile: file})
	if err == nil {
		t.Fatal("Load accepted a malformed configuration file")
	}

	for name, rendering := range errorRenderings(err) {
		if strings.Contains(rendering, sentinelSecret) {
			t.Errorf("%s rendering leaks the secret: %s", name, rendering)
		}
	}
}

// TestConfigFileErrorExposesOnlySentinels keeps the public chain limited to the
// sentinels this package authored: anything else could carry parser text.
func TestConfigFileErrorExposesOnlySentinels(t *testing.T) {
	file := writeConfigFile(t, malformedWithSecret)

	_, err := Load(LoadOptions{ConfigFile: file})
	if err == nil {
		t.Fatal("Load accepted a malformed configuration file")
	}

	if errors.Is(err, fs.ErrNotExist) {
		t.Error("the raw filesystem cause is reachable through the error chain")
	}
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		t.Error("errors.As extracts the raw *fs.PathError, whose text is not authored here")
	}
	if got := len(chainTexts(err)); got > 8 {
		t.Errorf("error chain renders %d texts, want only this package's own", got)
	}
}

// TestConfigFileErrorKeepsSentinelsMatchable preserves the documented behavior
// for both failure modes.
func TestConfigFileErrorKeepsSentinelsMatchable(t *testing.T) {
	cases := map[string]string{
		"malformed": writeConfigFile(t, malformedWithSecret),
		"missing":   filepath.Join(t.TempDir(), "absent.yaml"),
	}

	for name, file := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Load(LoadOptions{ConfigFile: file})
			if err == nil {
				t.Fatal("Load accepted an unusable configuration file")
			}
			for _, sentinel := range []error{ErrConfigFile, ErrInvalidConfig} {
				if !errors.Is(err, sentinel) {
					t.Errorf("error %v does not match %v", err, sentinel)
				}
			}
		})
	}
}

// TestConfigFileErrorLimitsPathRendering keeps the directory layout of the
// deployment out of the message while still naming the file the operator asked
// for.
func TestConfigFileErrorLimitsPathRendering(t *testing.T) {
	file := writeConfigFile(t, malformedWithSecret)
	dir := filepath.Dir(file)

	_, err := Load(LoadOptions{ConfigFile: file})
	if err == nil {
		t.Fatal("Load accepted a malformed configuration file")
	}

	for name, rendering := range errorRenderings(err) {
		if strings.Contains(rendering, dir) {
			t.Errorf("%s rendering contains the full directory %q: %s", name, dir, rendering)
		}
	}
	if got := err.Error(); !strings.Contains(got, filepath.Base(file)) {
		t.Errorf("Error() = %q, want it to name %q", got, filepath.Base(file))
	}
}

// TestConfigFileErrorRedactsAnUnusualFileName keeps a hostile or secret-bearing
// name out of the message, since the name is the only caller-supplied text left.
func TestConfigFileErrorRedactsAnUnusualFileName(t *testing.T) {
	t.Parallel()

	err := newConfigFileError("/etc/garmin/"+sentinelSecret+" key=value", errors.New("read failed"))

	for name, rendering := range errorRenderings(err) {
		if strings.Contains(rendering, sentinelSecret) {
			t.Errorf("%s rendering leaks the file name: %s", name, rendering)
		}
		if !strings.Contains(rendering, redactedPathMarker) {
			t.Errorf("%s rendering = %q, want the %q marker", name, rendering, redactedPathMarker)
		}
	}
}

// TestConfigFileNameSanitizing covers the rendering rules for the one piece of
// caller-supplied text left in the message.
func TestConfigFileNameSanitizing(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"/etc/garmin/garmin-mcp.yaml": "garmin-mcp.yaml",
		"  garmin_mcp.YAML  ":         "garmin_mcp.YAML",
		// filepath.Base drops a trailing separator, so a directory path renders as
		// the directory's own name. That is still a plain, bounded name.
		"/etc/garmin/":         "garmin",
		"":                     redactedPathMarker,
		"/":                    redactedPathMarker,
		"/etc/garmin/a b.yaml": redactedPathMarker,
		"/etc/garmin/kéy.yaml": redactedPathMarker,
		"/etc/" + strings.Repeat("n", maxConfigFileNameLen+1): redactedPathMarker,
	}

	for path, want := range tests {
		if got := configFileName(path); got != want {
			t.Errorf("configFileName(%q) = %q, want %q", path, got, want)
		}
	}
}

// TestConfigFileReasonClassification keeps the reason a property of the failure
// rather than of the parser's wording.
func TestConfigFileReasonClassification(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		cause error
		want  string
	}{
		"nil":       {cause: nil, want: reasonUnreadable},
		"missing":   {cause: fmt.Errorf("open: %w", fs.ErrNotExist), want: reasonMissing},
		"forbidden": {cause: fmt.Errorf("open: %w", fs.ErrPermission), want: reasonForbidden},
		"parse":     {cause: viper.ConfigParseError{}, want: reasonUnparsable},
		"unrelated": {cause: errors.New("read failed"), want: reasonUnreadable},
		"wrapped nil": {cause: fmt.Errorf("wrapped: %w", errors.New("io failure")),
			want: reasonUnreadable},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if got := configFileReason(tc.cause); got != tc.want {
				t.Errorf("configFileReason = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestConfigFileErrorStaysUseful is the counter-test: sanitizing must not reduce
// the message to nothing an operator can act on.
func TestConfigFileErrorStaysUseful(t *testing.T) {
	t.Parallel()

	missing := newConfigFileError("/etc/garmin/garmin-mcp.yaml", fmt.Errorf("open: %w", fs.ErrNotExist))
	broken := newConfigFileError("/etc/garmin/garmin-mcp.yaml", errors.New("yaml: line 2: did not find expected key"))

	for _, err := range []error{missing, broken} {
		text := err.Error()
		if !strings.Contains(text, "garmin-mcp.yaml") {
			t.Errorf("Error() = %q, want it to name the file", text)
		}
		if !strings.HasPrefix(text, "config: ") {
			t.Errorf("Error() = %q, want the config prefix", text)
		}
	}
	if missing.Error() == broken.Error() {
		t.Errorf("a missing file and an unparsable one report the same reason: %q", missing.Error())
	}
}

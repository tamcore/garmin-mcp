// Command notices regenerates THIRD_PARTY_NOTICES.md from the module cache.
//
// It is a maintenance tool, not part of the product. It is never linked into
// garmin-mcp, it is not reachable from ./cmd/garmin-mcp, and GoReleaser builds
// only that package, so this command adds nothing to a release artifact.
//
// This package stays thin for the same reason cmd/garmin-mcp does: the behavior
// lives in internal/notices, where it is tested without running a binary.
//
// Run it from the repository root after any dependency change:
//
//	go run ./cmd/notices
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/tamcore/garmin-mcp/internal/notices"
)

// noticesFile is the path the release archives and the container image expect.
const noticesFile = "THIRD_PARTY_NOTICES.md"

func main() {
	out := flag.String("o", noticesFile, "output file, or - for stdout")
	dir := flag.String("C", ".", "module root to resolve the dependency set from")
	flag.Parse()

	if err := run(context.Background(), *dir, *out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run generates the notices and writes them. Every failure is fatal by design:
// a licence that cannot be read must stop the run, never degrade into a
// placeholder in a file that ships as a distribution condition.
func run(ctx context.Context, dir, out string) error {
	generated, err := notices.Generate(ctx, notices.Options{Dir: dir})
	if err != nil {
		return err
	}

	if out == "-" {
		_, err := os.Stdout.Write(generated)

		return err
	}

	if err := os.WriteFile(out, generated, 0o644); err != nil {
		return fmt.Errorf("notices: writing %s: %w", out, err)
	}

	return nil
}

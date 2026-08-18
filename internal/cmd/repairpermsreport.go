//go:build unix

package cmd

import (
	"fmt"
	"io"
)

// writeRepairReport renders one line per target that was not already clean
// (problemNone) and not legitimately absent, plus a closing summary line.
//
// Nothing printed here ever carries file content: every value is a path this
// command was already told to check, a mode, or a uid.
func writeRepairReport(out io.Writer, dryRun bool, results []repairResult) {
	printed := 0
	for _, result := range results {
		if line, ok := repairLine(dryRun, result); ok {
			_, _ = fmt.Fprintln(out, line)
			printed++
		}
	}

	if printed == 0 {
		_, _ = fmt.Fprintln(out, "repair-permissions: nothing to do; every checked path is already owner-only")
		return
	}
	_, _ = fmt.Fprintf(out, "repair-permissions: %d path(s) reported above\n", printed)
}

// repairLine renders one result, and reports whether it should be printed at
// all: a clean pass and a legitimately absent target print nothing, so a
// healthy deployment's report is silent except for the closing summary.
func repairLine(dryRun bool, result repairResult) (string, bool) {
	target := result.finding.target
	switch result.finding.problem {
	case problemNone, problemAbsent:
		return "", false
	case problemWrongMode:
		return wrongModeLine(dryRun, result), true
	case problemForeignOwner, problemWrongType, problemInspectFailed:
		return fmt.Sprintf("refusing to touch %s: %s", target.path, result.finding.detail), true
	case problemEnumerationFailed:
		return fmt.Sprintf("cannot enumerate %s: %s", target.path, result.finding.detail), true
	default:
		return fmt.Sprintf("%s: unrecognized finding", target.path), true
	}
}

// wrongModeLine renders the one problem this command can actually act on, in
// whichever shape applies: a completed fix, a failed attempt, a fix withheld
// because a structural problem elsewhere must be resolved first, a target
// this command never auto-fixes at all (repairTarget.noGroupOrOther), or, in
// --dry-run, what a real run would do.
func wrongModeLine(dryRun bool, result repairResult) string {
	target := result.finding.target
	switch {
	case dryRun:
		return fmt.Sprintf("would tighten %s: %s", target.path, result.finding.detail)
	case result.fixErr != nil:
		return fmt.Sprintf("failed to tighten %s: %s: %v", target.path, result.finding.detail, result.fixErr)
	case result.fixed:
		return fmt.Sprintf("tightened %s: %s", target.path, result.finding.detail)
	case result.skippedForStructuralProblem:
		return fmt.Sprintf("not tightening %s: %s (left unchanged: another target has an "+
			"unresolved problem; resolve it and run again)", target.path, result.finding.detail)
	case target.noGroupOrOther:
		return fmt.Sprintf("not tightening %s: %s", target.path, result.finding.detail)
	default:
		// Unreachable through runRepairPermissions: a fixable problemWrongMode
		// finding with dryRun false and no structural block is always either
		// fixed or fixErr. Named rather than silently mis-stated, in case a
		// future caller reaches this function with a different repairOne.
		return fmt.Sprintf("%s: %s (not attempted)", target.path, result.finding.detail)
	}
}

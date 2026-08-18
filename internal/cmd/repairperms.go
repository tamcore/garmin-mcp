//go:build unix

package cmd

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/tamcore/garmin-mcp/internal/config"
)

// flagDryRun reports without changing anything.
const flagDryRun = "dry-run"

// NewRepairPermissionsCommand repairs the mode of every file and directory
// internal/securefile guards under the configured state directory (and,
// when configured, the database and any confidential OAuth client's
// secret-hash-file), without ever opening what it repairs for its content.
//
// It exists for the one upgrade path self-healing on load cannot cover: a
// Kubernetes fsGroup mount recursion that widens a file's mode is refused at
// load time on purpose, because a group-writable secret may have been
// modified rather than merely read (see docs/operations.md). That refusal
// is correct and this command does not weaken it — it gives an operator an
// explicit, offline, metadata-only way to fix state a platform widened
// before this build's ownership and mode checks existed, so an upgrade is
// not otherwise permanently blocked.
func NewRepairPermissionsCommand(opts Options) *cobra.Command {
	command := &cobra.Command{
		Use:   "repair-permissions",
		Short: "Bring the mode of state this process legitimately owns to exactly what serve requires",
		Long: "Inspect every file and directory internal/securefile guards under\n" +
			"the configured state directory — the key directory and its key\n" +
			"files, the active-key-version marker, the token directory and its\n" +
			"records, and, when database-path is set, the database and its\n" +
			"\"-wal\"/\"-shm\" sidecars — and tighten whichever of them this\n" +
			"process's effective user already owns to exactly the mode the\n" +
			"server itself requires — the same exact-mode check securefile\n" +
			"applies at load time. This most commonly narrows a mode that grants\n" +
			"group or other access, but it can also add an owner bit: a file the\n" +
			"server would refuse for being narrower than it requires (e.g. 0400\n" +
			"where 0600 is required) is brought up to that exact mode too.\n\n" +
			"Nothing here is opened for its content: every check is a stat, and\n" +
			"every fix is a chmod. The database is never opened as a database,\n" +
			"so this is safe to run beside a live serve process, and safe to run\n" +
			"before one starts, in an init container above all.\n\n" +
			"An object owned by a different local account is reported and left\n" +
			"untouched: this command can only legitimately fix what this\n" +
			"process's effective user already owns. If anything is left\n" +
			"unfixable, or --dry-run finds anything it would have fixed, the\n" +
			"command exits non-zero, so it is never mistaken for a green light.\n\n" +
			"Running it on already-healthy state is a clean no-op.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			dryRun, err := cmd.Flags().GetBool(flagDryRun)
			if err != nil {
				return err
			}
			return runRepairPermissions(cfg, dryRun, opts.stdout())
		},
	}

	command.Flags().Bool(flagDryRun, false, "report what would change without changing anything")
	return command
}

// repairResult is one target's finding plus what this run did about it, if
// anything.
type repairResult struct {
	finding finding
	// fixed reports whether this run actually tightened the target. It is
	// never true together with fixErr, and never true in --dry-run.
	fixed bool
	// fixErr is set when applyFix was attempted and failed.
	fixErr error
	// skippedForStructuralProblem reports that this finding was fixable, but
	// was deliberately left untouched because some other target in the same
	// run has an unresolved, unfixable problem — see runRepairPermissions.
	skippedForStructuralProblem bool
}

// runRepairPermissions inspects every guarded path under cfg and, unless
// dryRun, tightens whichever of them this process legitimately owns but finds
// too permissive.
//
// Inspection and fixing are two separate passes over the whole target set,
// not interleaved: every target is inspected first, and a fix is only ever
// applied once every target's finding is known and none of them is a
// structural problem — a foreign owner, the wrong object type, an object
// that could not be inspected, or a directory that could not be enumerated —
// this command cannot fix. A run that finds one of those partway through
// would otherwise have already chmodded everything inspected before it,
// leaving the deployment in a changed, still-unsafe, and now
// harder-to-reason-about state for a problem the run was always going to
// report as unresolved regardless. The one deliberate exception is resolving
// [dirScan]: a directory this process owns but cannot currently list must be
// tightened to discover what's inside it at all, which resolveScan does
// under its own narrow rules — see its doc comment.
func runRepairPermissions(cfg config.Config, dryRun bool, out io.Writer) error {
	targets, scans, err := repairTargets(cfg)
	if err != nil {
		return err
	}

	euid := currentEUID()
	findings := make([]finding, len(targets))
	byPath := make(map[string]int, len(targets))
	for i, target := range targets {
		findings[i] = inspectTarget(target, euid)
		byPath[target.path] = i
	}

	preFixed := make(map[string]bool)
	for _, scan := range scans {
		extra, failure, fixedEarly := resolveScan(scan, findings[byPath[scan.target.path]], dryRun)
		if fixedEarly {
			preFixed[scan.target.path] = true
		}
		if failure != nil {
			findings = append(findings, *failure)
			continue
		}
		for _, t := range extra {
			findings = append(findings, inspectTarget(t, euid))
		}
	}

	blockedByStructuralProblem := !dryRun && anyStructuralProblem(findings)

	results := make([]repairResult, len(findings))
	for i, f := range findings {
		if preFixed[f.target.path] {
			results[i] = repairResult{finding: f, fixed: true}
			continue
		}
		results[i] = repairOne(f, dryRun, blockedByStructuralProblem)
	}

	writeRepairReport(out, dryRun, results)
	if repairLeftSomethingUnresolved(dryRun, results) {
		return ErrPermissionsUnresolved
	}
	return nil
}

// anyStructuralProblem reports whether any finding is unresolved and not
// fixable: the kind of problem this command can only report, never repair,
// and which — per runRepairPermissions's doc comment — must be known before
// any fix is applied to anything else.
func anyStructuralProblem(findings []finding) bool {
	for _, f := range findings {
		if f.unresolved() && !f.fixable() {
			return true
		}
	}
	return false
}

// repairOne applies a fix for f when it is fixable, unless dryRun or blocked
// reports that a structural problem elsewhere must be resolved first.
func repairOne(f finding, dryRun, blocked bool) repairResult {
	result := repairResult{finding: f}
	if !f.fixable() || dryRun {
		return result
	}
	if blocked {
		result.skippedForStructuralProblem = true
		return result
	}
	if err := applyFix(f); err != nil {
		result.fixErr = err
		return result
	}
	result.fixed = true
	return result
}

// repairLeftSomethingUnresolved reports whether any result must make the
// command exit non-zero: every finding.unresolved() case is one, and, in
// --dry-run, a fixable finding is left exactly as unresolved as it was found,
// because dry-run changes nothing.
func repairLeftSomethingUnresolved(dryRun bool, results []repairResult) bool {
	for _, result := range results {
		if !result.finding.unresolved() {
			continue
		}
		if result.finding.fixable() && !dryRun && result.fixed {
			continue
		}
		return true
	}
	return false
}

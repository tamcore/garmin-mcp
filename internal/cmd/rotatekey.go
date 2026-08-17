package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/tamcore/garmin-mcp/internal/config"
	"github.com/tamcore/garmin-mcp/internal/cryptostore"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/store"
)

// flagTargetVersion is the only flag rotate-key accepts. There is no default: a
// guessed target would let a mistyped invocation silently rotate to the wrong
// version, and the one-version-at-a-time rule (ErrRotationTargetInvalid) needs an
// explicit number to check against.
const flagTargetVersion = "target-version"

// NewRotateKeyCommand rotates the encryption key to a new version and re-seals
// every stored record onto it.
//
// Rotation is offline and idempotent, following the same shape as migrate: this
// command reads the deployment's own configuration, never guesses a database
// location, and is safe to run more than once. Unlike migrate it does not require
// a database — a local stdio deployment rotates its single principal's FileStore
// record, and a remote deployment rotates every sealed column of its SQLite
// store, chosen the same way `serve` already picks a backend: DatabasePath set
// means SQLite, otherwise FileStore.
//
// The target key becomes active — and every new write from THIS process starts
// using it — before any re-sealing happens on a fresh rotation. That durable
// marker is also what makes a killed run resumable: a second invocation with the
// SAME --target-version reads back the marker it already wrote, recognizes the
// resume, and re-scans for what still needs re-sealing rather than re-activating
// anything or trusting how far the first run got. See checkRotationTarget.
func NewRotateKeyCommand(opts Options) *cobra.Command {
	command := &cobra.Command{
		Use:   "rotate-key",
		Short: "Rotate the encryption key and re-seal every stored record",
		Long: "Rotate to a new encryption key version and re-seal every stored\n" +
			"record onto it.\n\n" +
			"Rotation is offline: it is a one-shot command, not a background service,\n" +
			"and it is not meant to run alongside a live serve process. It is safe to\n" +
			"interrupt and safe to run again — a killed run resumes by re-scanning for\n" +
			"records still sealed under a retired key rather than trusting a record of\n" +
			"its own progress.\n\n" +
			"The retiring key must stay in place until this command reports every\n" +
			"record resealed. Deleting it earlier makes any record it still holds\n" +
			"unrecoverable. Retiring it afterwards is a manual, separate step this\n" +
			"command never takes for you.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			target, err := cmd.Flags().GetInt(flagTargetVersion)
			if err != nil {
				return fmt.Errorf("reading --%s: %w", flagTargetVersion, err)
			}
			return runRotateKey(cmd.Context(), cfg, target, opts.stdout())
		},
	}

	command.Flags().Int(flagTargetVersion, 0,
		"the key version to rotate to: the active version plus one to start a new rotation, "+
			"or the active version itself to resume one already in progress (required)")
	_ = command.MarkFlagRequired(flagTargetVersion)
	return command
}

// runRotateKey resolves the deployment's key state, opens the retiring and
// target keys, activates the target, and re-seals whichever backend this
// deployment uses.
func runRotateKey(ctx context.Context, cfg config.Config, target int, out io.Writer) error {
	paths, err := resolveStatePaths(cfg)
	if err != nil {
		return err
	}

	resuming, err := checkRotationTarget(paths, target)
	if err != nil {
		return err
	}

	// The backend and its principal are resolved before anything durable
	// happens: a FileStore deployment that binds no principal must be refused
	// before the marker moves, not after, or a rotation is left half-started
	// against a deployment that was never going to have anything to reseal.
	var principal identity.Principal
	if cfg.DatabasePath == "" {
		principal, _, err = bindPrincipal(cfg)
		if err != nil {
			return err
		}
	}

	// A resume whose retiring key is already gone is only safe when nothing is
	// left sealed under it: the documented sequence is rotate, confirm every
	// record resealed, THEN retire the key, and an operator who follows it and
	// re-runs rotate-key to double-check must see completion, not a refusal
	// that reads like data loss. loadRotationKeys itself cannot make that
	// distinction — it loads the retiring key unconditionally — so it is
	// checked here first, before that unconditional load ever runs.
	if resuming {
		retiringVersion := target - 1
		if _, err := cryptostore.LoadKey(paths.keys, retiringVersion); err != nil {
			if !errors.Is(err, cryptostore.ErrKeyNotFound) {
				return fmt.Errorf("opening the active key to rotate from: %w", err)
			}
			if rotationAlreadyComplete(ctx, cfg, paths, target, principal) {
				_, _ = fmt.Fprintln(out, completeWithoutRetiringKeyReport(
					cfg.DatabasePath != "", retiringVersion, target))
				return nil
			}
			return fmt.Errorf("opening the active key to rotate from: %w", err)
		}
	}

	targetKey, retired, err := loadRotationKeys(paths, target, resuming)
	if err != nil {
		return err
	}

	if !resuming {
		// This is the moment the mixed-version read window opens: every write
		// from here on is sealed under target, and reading a record still at
		// an older version now depends on the retired keys loaded above. A
		// resumed run already crossed this moment the first time; writing the
		// marker again here would be a harmless no-op, but skipping it keeps
		// this branch an honest description of when the window actually opened.
		if err := writeActiveKeyVersion(paths, target); err != nil {
			return fmt.Errorf("activating the target key version: %w", err)
		}
	}

	if cfg.DatabasePath != "" {
		return runRotateKeySQLite(ctx, cfg, targetKey, retired, out)
	}
	return runRotateKeyFileStore(ctx, principal, paths, targetKey, retired, out)
}

// checkRotationTarget validates target against the current active version and
// reports whether this invocation resumes a rotation a previous run already
// activated.
//
// A killed run already wrote the marker before it died, so the active version
// this reads back IS the target a first attempt was given. Refusing that as
// "not active+1" would make resuming impossible: the only way past the guard
// would mint a further key and strand whatever is still sealed under the
// original retiring key. Accepting target == active as a resume — continuing
// to re-seal without re-activating or minting anything — is what actually
// lets a killed run continue. Any other target is refused exactly as before.
func checkRotationTarget(paths statePaths, target int) (resuming bool, err error) {
	if target > cryptostore.MaxKeyVersion {
		return false, fmt.Errorf(
			"%w: target %d exceeds the maximum supported key version %d, which cannot be sealed",
			ErrRotationTargetInvalid, target, cryptostore.MaxKeyVersion)
	}

	active, present, err := resolveActiveKeyVersion(paths)
	if err != nil {
		return false, err
	}
	resuming = target == active
	if resuming && !present {
		// target == active only means "resume" when a marker actually recorded
		// that a rotation activated that version. Without one, active is only
		// the bootstrap default (defaultActiveKeyVersion) — a deployment that
		// has never rotated — so target == active here is a target equal to
		// where every record already is, which is nothing to rotate from, not a
		// rotation to resume. Without this check, loadRotationKeys would go on
		// to compute retiringVersion = target-1 = 0 and fail deep inside with
		// an opaque "invalid key version 0" instead of naming the real problem.
		return false, fmt.Errorf(
			"%w: there is nothing to rotate from: this deployment has never rotated "+
				"(no active-key-version marker), so target %d names the version everything "+
				"is already at; use target %d to start a new rotation",
			ErrRotationTargetInvalid, target, active+1)
	}
	if !resuming && target != active+1 {
		return false, fmt.Errorf(
			"%w: active version is %d, so the target must be %d to start a new rotation "+
				"or %d to resume one already in progress, got %d",
			ErrRotationTargetInvalid, active, active+1, active, target)
	}
	return resuming, nil
}

// openRotationTargetKey loads the target key when resuming a rotation and
// creates it only when starting one.
//
// The distinction is not cosmetic. A resumed run's target key already exists,
// because the interrupted run created it and sealed records under it, so an
// absent file means that key was LOST rather than that one should be minted.
// Creating a replacement here would leave every record the first run already
// re-sealed readable by nothing, with no way back — the same failure the active
// marker's own LoadKey/LoadOrCreateKey split exists to prevent.
func openRotationTargetKey(paths statePaths, target int, resuming bool) (cryptostore.Key, error) {
	if resuming {
		key, err := cryptostore.LoadKey(paths.keys, target)
		if err != nil {
			return cryptostore.Key{}, fmt.Errorf(
				"opening the target key of the rotation being resumed: %w", err)
		}
		return key, nil
	}
	key, err := cryptostore.LoadOrCreateKey(paths.keys, target)
	if err != nil {
		return cryptostore.Key{}, fmt.Errorf("opening the target encryption key: %w", err)
	}
	return key, nil
}

// rotationAlreadyComplete reports whether target alone (no retired keys) is
// enough to read and confirm every stored record is already sealed under it,
// for the one case that matters here: the retiring key is gone. Any failure —
// the target key itself missing, the backend failing to open, a record or row
// still needing a key this call refuses to load — is reported as NOT
// complete, so this fails closed exactly like the ordinary missing-key
// refusal it stands in for.
func rotationAlreadyComplete(
	ctx context.Context, cfg config.Config, paths statePaths, target int, principal identity.Principal,
) bool {
	targetKey, err := cryptostore.LoadKey(paths.keys, target)
	if err != nil {
		return false
	}

	if cfg.DatabasePath != "" {
		sqlite, err := store.OpenSQLite(ctx, store.SQLiteConfig{Path: cfg.DatabasePath, Key: targetKey})
		if err != nil {
			return false
		}
		defer func() { _ = sqlite.Close() }()
		remaining, err := sqlite.RemainingToReseal(ctx)
		return err == nil && remaining.Done()
	}

	files, err := store.NewFileStore(store.Config{Dir: paths.tokens, Key: targetKey})
	if err != nil {
		return false
	}
	outcome, err := files.Reseal(ctx, principal.ID())
	if err != nil {
		return false
	}
	return outcome == store.ResealNoRecord || outcome == store.ResealAlreadyCurrent
}

// completeWithoutRetiringKeyReport words the "retiring key already gone, nothing
// left to reseal" outcome for the backend that was actually scanned.
//
// The two backends can support very different claims, and saying more than was
// checked is how an operator ends up deleting a key a record still needs. The
// SQLite scan reads every sealed row, so it can speak for the store. A FileStore
// scan reads the ONE record this configuration's principal binds: record file
// names are one-way digests of the principal id, so a record belonging to a
// principal this configuration does not bind cannot be enumerated, let alone
// checked. That limit is stated rather than glossed.
func completeWithoutRetiringKeyReport(sqlite bool, retiringVersion, target int) string {
	gone := "the retiring key (version " + strconv.Itoa(retiringVersion) + ") is gone"
	if sqlite {
		return gone + ", and no sealed row still needs it: rotation to version " +
			strconv.Itoa(target) + " is already complete"
	}
	return gone + ", and the bound principal's record does not need it: rotation to version " +
		strconv.Itoa(target) + " is complete for that record. A record for a principal this " +
		"configuration does not bind cannot be checked and is not covered by this statement"
}

// loadRotationKeys opens the retiring key that must still be able to read
// unmigrated records, opens or creates the target key, and gathers every
// older retired key still on disk.
//
// retiringVersion is target-1 in both the fresh and the resumed case: on a
// fresh rotation the active version equals target-1, and on a resume the
// active version already equals target, so the version whose key must still
// be able to read what has not been re-sealed yet is always one below target.
func loadRotationKeys(
	paths statePaths, target int, resuming bool,
) (cryptostore.Key, []cryptostore.Key, error) {
	retiringVersion := target - 1
	retiring, err := cryptostore.LoadKey(paths.keys, retiringVersion)
	if err != nil {
		return cryptostore.Key{}, nil, fmt.Errorf("opening the active key to rotate from: %w", err)
	}
	targetKey, err := openRotationTargetKey(paths, target, resuming)
	if err != nil {
		return cryptostore.Key{}, nil, err
	}
	olderRetired, err := loadRetiredKeys(paths, retiringVersion)
	if err != nil {
		return cryptostore.Key{}, nil, err
	}
	return targetKey, append([]cryptostore.Key{retiring}, olderRetired...), nil
}

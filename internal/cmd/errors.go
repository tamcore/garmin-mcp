package cmd

import "errors"

// Start-up failure sentinels. Each is comparable with errors.Is, because a caller
// acts on the distinction: unresolvable state is a mistake in the deployment
// layout, unsupported key material is a mistake in the secret plumbing, and
// neither is a defect in the subsystem that reported it.
var (
	// ErrUnresolvedState reports that no state directory could be determined:
	// none was configured and the platform's per-user configuration directory is
	// unavailable. Nothing is guessed, because a guessed location silently splits
	// an operator's tokens across two directories.
	ErrUnresolvedState = errors.New("no state directory could be resolved")

	// ErrUnsupportedKeyMaterial reports key material this build cannot honor.
	// Inline master key material is the current case: internal/cryptostore owns
	// key installation and exposes no way to adopt caller-supplied material, so
	// accepting the setting would mean serving under a key the operator did not
	// supply. The error never echoes the material.
	ErrUnsupportedKeyMaterial = errors.New("unsupported encryption key material")

	// ErrInsecureDeployment reports a remote deployment this build refuses to
	// serve, such as a cleartext public URL. It is separate from the
	// configuration package's own refusals because it is decided by the
	// subsystems being assembled — the authorization server will not name a
	// cleartext issuer, and the transport will not serve a cleartext public bind
	// — rather than by the lexical checks that ran before anything was opened.
	ErrInsecureDeployment = errors.New("the remote deployment is not safe to serve")

	// ErrUnregisteredClient reports a configured OAuth client that has no
	// registration in the database.
	//
	// A client registration lives in two places on purpose: the database holds
	// the identity and the exact redirect URIs, and configuration holds the OAuth
	// policy the database has no column for. An authorization transaction
	// references the database row, so a client that exists only in configuration
	// can authorize nobody.
	ErrUnregisteredClient = errors.New("the configured OAuth client is not registered in the database")

	// ErrNoGarminAccount reports a login Garmin accepted but attributed to no
	// account.
	//
	// A remote deployment keys its isolation on the Garmin account, so without one
	// there is nothing to key on. Falling back to the email would key isolation on
	// exactly the value that must never be the boundary, so the login is refused
	// instead and no principal is created.
	ErrNoGarminAccount = errors.New("the Garmin login named no account to isolate on")

	// ErrNoDatabasePath reports that a command needing the multi-user database
	// was given no location for it.
	//
	// Nothing is defaulted here on purpose. A guessed location would create an
	// empty database next to whatever the process happened to be started from,
	// which then migrates cleanly, serves nobody's data, and looks like success
	// until the real database is found still unmigrated.
	ErrNoDatabasePath = errors.New("no database path is configured")

	// ErrUnsafeDeployment reports that a diagnostic check found something that
	// exists and must not be used, such as key material another local account can
	// read. The report names each finding; this sentinel is what makes the
	// command exit non-zero so a script notices.
	ErrUnsafeDeployment = errors.New("the deployment has a check that must be fixed before serving")

	// ErrRotationTargetInvalid reports a --target-version that is not exactly one
	// past the active key version. Rotation moves one version at a time on
	// purpose: skipping a version would leave nothing that ever held the
	// intermediate key, which is unrecoverable if any record turns out to still
	// need it.
	ErrRotationTargetInvalid = errors.New("rotate-key: target version must be exactly the active version plus one")

	// ErrRotationIncomplete reports that a reseal pass ended with a sealed
	// record still left at a retired key version. It is not a crash: the active
	// key version was already recorded, so running rotate-key again resumes and
	// finishes the remaining records. The retiring key must stay in place until a
	// run reports none of this.
	ErrRotationIncomplete = errors.New("rotate-key: some records still need a retired key; run rotate-key again")

	// ErrPermissionsUnresolved reports that repair-permissions found something
	// it could not (or, in --dry-run, did not) leave in a safe state: a file or
	// directory owned by another local account, an object of the wrong type
	// where a file or directory belongs, an entry it could not inspect, a
	// tightening attempt that failed, or — in --dry-run only — a mode it would
	// have tightened. It is what makes the command exit non-zero so a script,
	// or an init container, notices rather than proceeding on a state that is
	// still unsafe.
	ErrPermissionsUnresolved = errors.New(
		"repair-permissions: the deployment's state directory still has a permission problem")
)

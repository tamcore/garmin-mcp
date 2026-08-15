//go:build garminlive

// Package live is the opt-in suite that runs against the real Garmin Connect
// service.
//
// It exists because a fixture cannot detect a wrong derivation: a fixture built
// from a test's own declared values agrees with any derivation of those values, so
// a session window that collapses to one sample and an ascent that comes out at
// roughly twice the device's own figure both pass a complete synthetic suite. See
// docs/adr/0007-fit-decoding-library.md.
//
// The suite therefore asserts **cross-source consistency**, never a golden value.
// Every check compares two sources Garmin itself provides — the decoded device
// file against the activity summary, a tool result against the domain client that
// backs it — or asserts an invariant that holds for any account. No distance, heart
// rate, activity name, date or identifier of the account under test appears in this
// package, and no failure message prints a reading: a failure names the field and
// the relative delta and stops there.
//
// Three gates must all be open before anything is dispatched, and a missing gate is
// a skip rather than a failure:
//
//   - the garminlive build tag,
//   - GARMIN_USERNAME and GARMIN_PASSWORD for a dedicated non-primary account,
//   - GARMIN_LIVE_ACK set to the exact acknowledgement value.
//
// The suite is read-only by construction, not merely by convention: the caller the
// tools and the domain clients are built on refuses any request that is not a GET or
// a HEAD, so no write or destructive tool can reach Garmin from here even if one
// were called. There is no mutation path in this package.
//
// Nothing is written outside a temporary directory that is removed when the suite
// ends: the token store, the encryption key and every other piece of state live
// there, so the maintainer's own token store and configuration are never touched.
// No response body, FIT file or fixture is recorded to disk.
//
// It never runs in CI. No workflow builds this tag.
package live

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
// A fourth gate, GARMIN_LIVE_WRITE_ACK, additionally enables the write half. It is
// separate and default off, so acknowledging live traffic never acknowledges live
// mutation, and with it shut the read half behaves exactly as it does on its own.
//
// The package has two halves, and each one is bounded by construction rather than by
// convention.
//
// The read half is read-only: the caller its tools and domain clients are built on
// refuses any request that is not a GET or a HEAD, apart from a POST whose body is
// one of the GraphQL query documents internal/garmin/client itself renders. Garmin's
// whole GraphQL surface sits behind one path, so the document is what is judged and
// not the path: a mutation reaching that path is refused like any other write.
//
// The write half has a caller of its own that refuses any mutating request whose
// target is not an object this suite created, before the request leaves the process.
// The recognised endpoint set is an allowlist, so an unrecognised mutating endpoint is
// refused rather than waved through.
//
// Ownership cannot be declared. The ledger has no unconditional entry point: an object
// enters it from the identifier Garmin returned in its create response, from a calendar
// entry read back that names an already-owned workout, or from a name that parses as
// one this suite generated on an earlier run. Every created object is removed by
// t.Cleanup and again at the end of the suite, and each batch removal reads the object
// back and only then releases it, so a silent no-op delete is a failure rather than a
// clean report.
//
// Nothing is written outside a temporary directory that is removed when the suite
// ends: the token store, the encryption key and every other piece of state live
// there, so the maintainer's own token store and configuration are never touched.
// No response body, FIT file or fixture is recorded to disk.
//
// It never runs in CI. No workflow builds this tag.
package live

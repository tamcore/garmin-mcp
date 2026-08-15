//go:build garminlive

package live

import (
	"slices"
	"strconv"
	"strings"
	"time"
)

// This file is how a name decides what may be deleted.
//
// It is separate from the ledger because it answers a different question. The ledger
// records what this run created and watched Garmin confirm; this file reads a name off
// an object nobody in this process created and decides whether an *earlier* run of this
// suite wrote it. That is the sweeper's only licence to touch anything on the account,
// so the parsing is exact, the two numeric fields must be spelled the way the generator
// spells them, and the run stamp must fall strictly between this suite's birth and the
// second this run began.

// hasSuitePrefix reports whether a name merely starts with the reserved prefix.
//
// It is the *skip* test, never the removal test, and the asymmetry is the point: a
// reader that skips one object too many loses nothing, while a sweeper that removes
// one object too many is a defect. isPreviousRunObject is what a removal asks.
func hasSuitePrefix(name *string) bool {
	return name != nil && strings.HasPrefix(*name, objectPrefix)
}

// nameStampFloor is the earliest run stamp a suite name may carry.
//
// It is the instant this write layer first existed — the author date of the commit that
// introduced it, `9e82609`, 2026-08-15T14:44:33Z — and deliberately not the midnight
// before it. A floor earlier than the suite's own first run admits stamps no run of it
// could have written, which is the whole population of accidents the floor exists to
// exclude, and a floor set to the start of the birth day admitted almost fifteen hours
// of them. Anything at or before it — a version, a count, an index, a stamp from a
// second when this suite could not have created anything — is not a run stamp.
//
// The residual is stated rather than glossed: a leftover of a *pre-commit* development
// run of the write half is no longer sweepable. Such an object still carries the
// reserved prefix and is therefore identifiable by hand, and the direction of the
// trade is the safe one — a floor that is too late leaves an object of this suite's on
// the account, while a floor that is too early lets the sweeper delete somebody's data.
//
// It is a function rather than a variable because a package-level variable is mutable
// state, which AGENTS.md forbids, and time.Date cannot be a constant.
func nameStampFloor() time.Time {
	return time.Date(2026, time.August, 15, 14, 44, 33, 0, time.UTC)
}

// isPreviousRunObject reports whether a name marks an object an *earlier* run of this
// suite created.
//
// It is the sweeper's only licence to touch anything, so it is deliberately more than a
// prefix test. A prefix is not ownership: it is a string a human could type. The name
// must be exactly what nameSequence renders and nothing else — the reserved prefix, one
// of the labels this suite declares, a run stamp between the floor and the instant this
// run began, and a counter that is a positive integer. Each of those rules excludes a
// shape no generated name has: an empty or unknown label, a stamp from before this suite
// existed or from this run, a zero or negative counter, and any spelling of an integer
// strconv.FormatInt does not produce.
//
// The two numeric fields are read through canonicalInt rather than through ParseInt
// directly, and that is the difference between "this parses as a number" and "this suite
// wrote this". ParseInt accepts a leading plus and any amount of zero padding, so "+1",
// "01" and "0000000001" all read as the counter 1 — none of which the generator can
// emit, and each of which is a shape an unrelated naming scheme reaches by accident.
//
// The cut-off is compared at the resolution the name carries. A generated name stamps
// whole seconds, while the instant a run starts carries nanoseconds, so an unrounded
// comparison reads a concurrent run's name — stamped at the same second, sub-second
// earlier — as strictly earlier than this run and sweeps a live run's objects out from
// under it. Truncating the cut-off to the second the name is written at makes the two
// runs compare equal, and equal is not earlier.
//
// What it still cannot do is prove that this suite created the object, and that is
// stated rather than glossed: a name is evidence about a name. A person who wrote an
// object called "garmin-mcp-live-<a declared label>-<a unix second since this suite
// existed>-<a positive integer>" by hand would have it removed. That is a name nobody
// produces by accident, the prefix is documented as reserved, and the suite runs only
// against a dedicated account, so the residual is accepted. What is *not* accepted, and
// what this function exists to prevent, is any pre-existing object being adopted merely
// because it starts with the prefix.
func isPreviousRunObject(name *string, before time.Time) bool {
	if name == nil {
		return false
	}
	rest, found := strings.CutPrefix(*name, objectPrefix)
	if !found {
		return false
	}

	parts := strings.Split(rest, "-")
	if len(parts) < 3 {
		return false
	}
	if counter, ok := canonicalInt(parts[len(parts)-1]); !ok || counter <= 0 {
		return false
	}
	if !slices.Contains(suiteLabels(), nameLabel(strings.Join(parts[:len(parts)-2], "-"))) {
		return false
	}
	seconds, ok := canonicalInt(parts[len(parts)-2])
	if !ok {
		return false
	}

	stamp := time.Unix(seconds, 0).UTC()
	return stamp.After(nameStampFloor()) && stamp.Before(before.Truncate(time.Second))
}

// canonicalInt reads one numeric field of a generated name, and reports false for every
// spelling strconv.FormatInt cannot produce.
//
// Round-tripping the parsed value back to text and requiring the original is the whole
// check: it rejects a leading plus, zero padding, and any other decoration ParseInt is
// happy to accept, because none of them is a string this suite's own generator can
// write. A field it cannot have written is not a field it wrote.
func canonicalInt(field string) (int64, bool) {
	value, err := strconv.ParseInt(field, 10, 64)
	if err != nil || strconv.FormatInt(value, 10) != field {
		return 0, false
	}
	return value, true
}

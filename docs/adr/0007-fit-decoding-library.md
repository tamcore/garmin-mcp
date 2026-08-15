# ADR 0007 — FIT decoding library

## Status

Accepted, 2026-08-15. `internal/garmin/api` decodes activity files with
`github.com/muktihari/fit` v0.28.3. The hand-rolled container decoder
(`fitdecode.go` and `fitvalue.go`) is deleted.

## Context

`get_activity_fit_data` and `get_power_duration_curve` read the device FIT file
that Garmin serves for the original format. Until now this package decoded that
file itself: a header reader, a definition- and data-record walker, the seventeen
base types with their per-type invalid sentinels, developer-field skipping,
compressed timestamp headers and both byte orders.

That decoder read the *container*. The container is a public binary format and can
be implemented from its published description. The FIT **profile** is a different
thing: it says which field number on which global message carries which quantity,
at which scale, offset and unit. It is distributed as a spreadsheet in Garmin's
SDK, and it cannot be verified by reading. The previous code said so plainly and
refused to read any session or lap summary field, deriving every figure from the
record stream instead. `FITSegment`'s doc comment called that a guard against "a
profile guess becoming a reported measurement".

The guard was honest and the result was still wrong. Measured against the
maintainer's own activities and Garmin's own summary of them:

1. **Session segments collapsed.** `AnalyzeFIT(...).Sessions[0]` reported
   `seconds=0, distance=0, samples=1` for every real file. The cause was not a
   decoding error: these devices write the *same* instant into `session.timestamp`
   and `session.start_time`, so the window `[start_time, timestamp]` is a point.
   The profile carries `total_elapsed_time`, which is the authority the derivation
   needed and which the old code had refused to read.
2. **Ascent roughly doubled.** Whole-activity ascent came out 116.8 m against
   Garmin's 63, 618.8 against 287, and 163.8 against 125 — consistently near
   double. Summing every positive delta of a one-second barometric altitude series
   re-adds a few decimetres of sensor jitter per sample. The profile carries
   `total_ascent`, computed by the device that owns the barometer.

Both defects share one cause: a decoder that can read bytes but is not allowed to
read meaning must reconstruct meaning, and its reconstruction is worse than the
device's own. The synthetic fixtures never caught either one, because a fixture
built from a test's declared values agrees with any derivation of them.

## Decision

Take the profile from a library instead of guessing it or refusing it.

`github.com/muktihari/fit` ships the official profile as generated Go: a typed
struct per message, with the scale and offset in the field comment and a
`…Scaled()` accessor that applies them and reports the type's invalid sentinel.
`mesgdef.Session.TotalDistance` is `uint32 // Scale: 100; Units: m`;
`mesgdef.Lap.AvgHeartRate` is field 15 where `mesgdef.Session.AvgHeartRate` is
field 16. That difference is real, it is the kind of thing a hand-written table
gets wrong once and then reports as a measurement, and it is not a difference this
project can independently verify.

### Why this library rather than `tormoder/fit`

`tormoder/fit` is the other established Go FIT implementation.

| | `muktihari/fit` | `tormoder/fit` |
|---|---|---|
| Latest release | v0.28.3, 2026-08-11 | 2023-10-01 |
| Commits in the last year | 62 | 2 |
| FIT Protocol V2 | supported | not supported |
| Licence | BSD-3-Clause | BSD-2-Clause |
| Transitive modules linked | none | none |
| Streaming listener API | yes | no |

An unmaintained decoder for a format whose profile Garmin still revises is the
same problem as a hand-rolled one, one step removed. Protocol V2 support matters
because developer fields and the newer message set appear in current device files.
The listener API matters because it is what lets this server keep its own bounds;
see below.

`tamcore/kadence` already depends on `github.com/muktihari/fit`, so the module is
already in the maintainer's review surface, and a drift or an advisory in it is
noticed once rather than twice.

### What is deliberately not delegated

The library decodes. It does not decide what this server is willing to decode, and
none of the following moves into it:

- **Bounds.** `FITLimits` stays this server's own. `MaxBytes` is checked in
  `extractFIT` before the decoder is constructed. `MaxMessages` is counted in
  `fitCollector.OnMesg`, and exceeding it cancels the context that
  `Decoder.DecodeWithContext` observes, so an oversized file is abandoned rather
  than read to its end. `MaxRecords` bounds what is retained and sets
  `RecordsTruncated`. `decoder.WithBroadcastOnly` is what makes this possible: the
  decoder broadcasts each message and retains none, so the only thing that grows
  with the file is what this package chose to keep.
- **Zip handling.** Garmin serves the original format as a zip archive or as a
  bare FIT file. `extractFIT` handles both and expands the archive entry under the
  byte bound, so a compression bomb is refused at the bound.
- **Coordinate suppression.** State this precisely, because the imprecise version was
  in this file and was wrong. The library **does** decode `record.position_lat` and
  `record.position_long`: `decoder.New` with a message listener decodes every field of
  every message, and the decoder exposes no field filter that would let a caller opt
  out. Field-level filtering before unmarshalling was evaluated against the SDK's
  options — `WithFactory` is the only hook anywhere near it, and a factory that omitted
  the two fields would leave them decoded as *unknown* fields rather than not decoded —
  so there is nothing to buy at any cost. What this package guarantees is therefore
  narrower, and it is what the code enforces: the two fields are **never read** into
  `FITRecord`; `fitCollector.scrubRecord` empties the reused `mesgdef.Record` after
  every sample, so no coordinate outlives one `OnMesg` call even inside the collector;
  and no returned structure, log line or error can carry a position. A per-second
  position series is the most sensitive thing in the file and no summary here needs it.

  The scrub covers four fields, not two, and the two extra ones are the reason a
  second review had to find this twice. `mesgdef.Record` also carries `UnknownFields`
  — every field number the profile does not define — and `DeveloperFields`, which an
  application names and describes itself. Either can carry a latitude, both alias the
  decoder's own message, and no method on the struct suppresses them, so clearing only
  `PositionLat` and `PositionLong` left a coordinate-bearing custom field sitting in
  the collector after the sample was read.

  The claim is split across two tests because it has two halves.
  `TestParseFITReturnsNoCoordinates` decodes a file carrying a synthetic track and
  asserts that neither the semicircle value nor the degrees it renders as appear
  anywhere in the decoded model or in its analysis.
  `TestCollectorScrubsEveryFieldAPositionCouldHideIn` builds a record carrying a
  position in all four places and asserts the retained struct holds none of them —
  which a test reading the returned model cannot see.
- **Error sanitization.** The library's errors carry a byte position and would grow
  to carry more. None of that text is reproduced. `fitCollector.fail` classifies
  the failure as `client.ErrMalformedPayload` or `client.ErrResponseTooLarge` and
  writes its own message.

The analytics the profile does not carry stay local and keep reading the decoded
record stream: `PowerDurationCurve`, climb detection, grade bands, the temperature
split, heart-rate drift and shift classification.

### Where a derived figure survives

A profile figure is preferred wherever the file carries one; a derived figure is
the fallback for a file that does not. The fallback ascent is no longer a naive
sum: it banks a rise only once it clears a three-metre threshold above the lowest
point since the last banked rise, so jitter cancels instead of accumulating. Climb
gain is now the net height across the detected run rather than a sum of per-sample
deltas, which is what a single sustained rise means.

## Consequences

- Real files now reproduce Garmin's own summary exactly for distance, elapsed
  time, average and peak heart rate, ascent and calories.
- Session and lap windows are derived as `start_time + total_elapsed_time`, with
  the recorded timestamp used only when it is later. This is a deviation from what
  the profile says `timestamp` means, and it is taken because real files disagree
  with the profile. `spanEnd` documents it and
  `TestSpanEndPrefersTheElapsedTime` pins it.
- `FITSpan` grew the profile summary fields and `FITSegment` grew `Calories`.
  `FITSegmentView` grew `calories_kcal`. The pinned *input* contracts in
  `compat/tools.json` are untouched.
- The synthetic fixture had to be fixed rather than the check weakened: it emitted
  a zero checksum, which the hand-rolled decoder ignored and the SDK verifies.
  `testkit.FITContainer` now computes a real FIT CRC.
- The library requires a field to carry the profile's base type. The deleted
  `fitvalue_test.go` wrote record fields under types the profile does not assign
  them — power as `float32`, distance as `uint64` — and asserted they decoded. No
  device writes such a file, and that coverage is the library's now.
- One module is added and no transitive module is linked. `golang.org/x/text` and
  `golang.org/x/sync` move up one minor version by minimal version selection.

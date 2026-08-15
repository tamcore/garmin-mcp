package api_test

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// fitContainer wraps a record stream in the FIT header and the checksum the format
// defines. The checksum is real: the SDK verifies it, and a fixture that emitted a
// zero one would only be proving that a lenient reader accepted it.
func fitContainer(body []byte) []byte { return testkit.FITContainer(body) }

// fitStamp is a synthetic FIT date_time for the tests that hand-build a stream.
const fitStamp = 1_136_073_600

// TestParseFITReadsABigEndianDefinition proves the architecture byte is honoured: a
// device that writes big-endian records must decode to the same readings.
func TestParseFITReadsABigEndianDefinition(t *testing.T) {
	t.Parallel()

	body := []byte{0x40, 0, 1}
	body = binary.BigEndian.AppendUint16(body, 20)
	body = append(body, 2, 253, 4, 0x86, 7, 2, 0x84)
	body = append(body, 0x00)
	body = binary.BigEndian.AppendUint32(body, fitStamp)
	body = binary.BigEndian.AppendUint16(body, 321)

	activity, err := api.ParseFITActivity(t.Context(), fitContainer(body), api.FITLimits{})
	if err != nil {
		t.Fatalf("ParseFITActivity() = %v", err)
	}
	if len(activity.Records) != 1 {
		t.Fatalf("%d records, want 1", len(activity.Records))
	}
	if got := activity.Records[0].Power; !got.OK || got.Value != 321 {
		t.Errorf("power = %+v, want 321", got)
	}
}

// TestParseFITStepsOverDeveloperFields proves an application-defined field is
// skipped by its declared width rather than interpreted.
func TestParseFITStepsOverDeveloperFields(t *testing.T) {
	t.Parallel()

	body := []byte{0x40 | 0x20, 0, 0}
	body = binary.LittleEndian.AppendUint16(body, 20)
	body = append(body, 2, 253, 4, 0x86, 7, 2, 0x84)
	body = append(body, 1, 0, 2, 0) // one developer field, two bytes wide
	body = append(body, 0x00)
	body = binary.LittleEndian.AppendUint32(body, fitStamp)
	body = binary.LittleEndian.AppendUint16(body, 250)
	body = append(body, 0xAB, 0xCD)

	activity, err := api.ParseFITActivity(t.Context(), fitContainer(body), api.FITLimits{})
	if err != nil {
		t.Fatalf("ParseFITActivity() = %v", err)
	}
	if len(activity.Records) != 1 {
		t.Fatalf("%d records, want 1", len(activity.Records))
	}
	if got := activity.Records[0].Power; !got.OK || got.Value != 250 {
		t.Errorf("power = %+v, want 250", got)
	}
}

// TestParseFITReadsACompressedTimestampHeader proves the five-bit offset form
// carries the record forward from the last full timestamp.
func TestParseFITReadsACompressedTimestampHeader(t *testing.T) {
	t.Parallel()

	body := []byte{0x40, 0, 0}
	body = binary.LittleEndian.AppendUint16(body, 20)
	body = append(body, 2, 253, 4, 0x86, 7, 2, 0x84)
	body = append(body, 0x00)
	body = binary.LittleEndian.AppendUint32(body, fitStamp)
	body = binary.LittleEndian.AppendUint16(body, 100)

	// A second slot without a timestamp field, which is the layout a compressed
	// record uses: the instant comes from the header instead.
	body = append(body, 0x41, 0, 0)
	body = binary.LittleEndian.AppendUint16(body, 20)
	body = append(body, 1, 7, 2, 0x84)
	body = append(body, 0x80|0x20|0x05) // local slot 1, offset five seconds
	body = binary.LittleEndian.AppendUint16(body, 110)

	activity, err := api.ParseFITActivity(t.Context(), fitContainer(body), api.FITLimits{})
	if err != nil {
		t.Fatalf("ParseFITActivity() = %v", err)
	}
	if len(activity.Records) != 2 {
		t.Fatalf("%d records, want 2", len(activity.Records))
	}
	if gap := activity.Records[1].Time.Sub(activity.Records[0].Time).Seconds(); gap != 5 {
		t.Errorf("the compressed record is %v seconds later, want 5", gap)
	}
}

// TestParseFITRefusesWhatIsNotAFITFile keeps a wrong download from being decoded as
// readings, and keeps every refusal in a classified error class.
func TestParseFITRefusesWhatIsNotAFITFile(t *testing.T) {
	t.Parallel()

	cases := map[string][]byte{
		"too short":     []byte("nope"),
		"no signature":  append([]byte{12, 0x20, 0, 0, 0, 0, 0, 0}, []byte("XXXXnnnn")...),
		"empty records": fitContainer(nil),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := api.ParseFITActivity(t.Context(), data, api.FITLimits{})
			if !errors.Is(err, client.ErrMalformedPayload) {
				t.Errorf("ParseFITActivity() = %v, want ErrMalformedPayload", err)
			}
		})
	}
}

// TestParseFITRefusesATruncatedRecord proves a stream that ends inside a message is
// reported rather than decoded into a half-read reading.
func TestParseFITRefusesATruncatedRecord(t *testing.T) {
	t.Parallel()

	body := []byte{0x40, 0, 0}
	body = binary.LittleEndian.AppendUint16(body, 20)
	body = append(body, 2, 253, 4, 0x86, 7, 2, 0x84)
	body = append(body, 0x00, 0x01, 0x02) // a data message that stops mid-field

	_, err := api.ParseFITActivity(t.Context(), fitContainer(body), api.FITLimits{})
	if !errors.Is(err, client.ErrMalformedPayload) {
		t.Errorf("ParseFITActivity() = %v, want ErrMalformedPayload", err)
	}
}

// TestParseFITRefusesAnUndefinedMessageSlot proves a data record that names a slot
// no definition filled is refused instead of being read at an arbitrary width.
func TestParseFITRefusesAnUndefinedMessageSlot(t *testing.T) {
	t.Parallel()

	_, err := api.ParseFITActivity(t.Context(), fitContainer([]byte{0x03, 0x00}), api.FITLimits{})
	if !errors.Is(err, client.ErrMalformedPayload) {
		t.Errorf("ParseFITActivity() = %v, want ErrMalformedPayload", err)
	}
}

// TestParseFITRefusesAFileOverItsByteBound is the size bound: a file larger than the
// configured ceiling is refused before it is decoded.
func TestParseFITRefusesAFileOverItsByteBound(t *testing.T) {
	t.Parallel()

	file := testkit.FITFile{Samples: rideSamples(60)}.Bytes()
	_, err := api.ParseFITActivity(t.Context(), file, api.FITLimits{MaxBytes: 32})
	if !errors.Is(err, client.ErrResponseTooLarge) {
		t.Errorf("ParseFITActivity() = %v, want ErrResponseTooLarge", err)
	}
}

// TestParseFITRefusesAFileOverItsMessageBound is the message bound, which is what
// stops a file that is small on the wire but enormous once decoded.
func TestParseFITRefusesAFileOverItsMessageBound(t *testing.T) {
	t.Parallel()

	file := testkit.FITFile{Samples: rideSamples(60)}.Bytes()
	_, err := api.ParseFITActivity(t.Context(), file, api.FITLimits{MaxMessages: 4})
	if !errors.Is(err, client.ErrResponseTooLarge) {
		t.Errorf("ParseFITActivity() = %v, want ErrResponseTooLarge", err)
	}
}

// TestParseFITStopsCollectingAtTheRecordBound proves the retained sample count is
// bounded and that the result says so rather than pretending the ride was short.
func TestParseFITStopsCollectingAtTheRecordBound(t *testing.T) {
	t.Parallel()

	file := testkit.FITFile{Samples: rideSamples(60)}.Bytes()
	activity, err := api.ParseFITActivity(t.Context(), file, api.FITLimits{MaxRecords: 10})
	if err != nil {
		t.Fatalf("ParseFITActivity() = %v", err)
	}
	if len(activity.Records) != 10 {
		t.Errorf("%d records, want the bound of 10", len(activity.Records))
	}
	if !activity.RecordsTruncated {
		t.Error("RecordsTruncated = false, want the bound reported")
	}
}

// spanFile builds a file whose lap count is deliberately absurd: every lap spans the
// whole record stream, which is the shape that makes the analysis quadratic. A device
// writes a few hundred laps at most, so a file like this is malformed or hostile.
func spanFile(seconds, laps int) []byte {
	windows := make([]testkit.FITLapFixture, 0, laps)
	for range laps {
		windows = append(windows, testkit.FITLapFixture{StartSecond: 0, EndSecond: seconds - 1})
	}
	return testkit.FITFile{Sport: 2, Session: true, Samples: rideSamples(seconds), Laps: windows}.Bytes()
}

// TestParseFITStopsCollectingAtTheSpanBound proves the session and lap counts are
// bounded during collection rather than on the rendered result.
//
// Every span is summarized against the whole record stream, so the cost of the
// analysis is the product of the two counts. Bounding the spans only when the result
// is rendered would leave that product unbounded, which is what this asserts against:
// the collected count stops at the bound, the decode says it truncated, and the call
// returns.
func TestParseFITStopsCollectingAtTheSpanBound(t *testing.T) {
	t.Parallel()

	const (
		seconds = 200
		laps    = 500
		maxLaps = 10
	)
	activity, err := api.ParseFITActivity(
		t.Context(), spanFile(seconds, laps), api.FITLimits{MaxLaps: maxLaps})
	if err != nil {
		t.Fatalf("ParseFITActivity() = %v", err)
	}
	if len(activity.Laps) != maxLaps {
		t.Errorf("%d laps, want the bound of %d", len(activity.Laps), maxLaps)
	}
	if !activity.SpansTruncated {
		t.Error("SpansTruncated = false, want the bound reported")
	}

	// The analysis runs over the bounded collection, so it summarizes exactly the
	// spans that survived the bound and no more.
	if summary := api.AnalyzeFIT(activity); len(summary.Laps) != maxLaps {
		t.Errorf("the analysis produced %d lap segments, want the bounded %d",
			len(summary.Laps), maxLaps)
	}
}

// TestParseFITAppliesTheDefaultSpanBound proves the bound is on by default, so a
// caller that declares no limits is not the unbounded case.
//
// The figure matters as much as its existence. Every span costs another pass over the
// whole retained record stream, so a default set at what a device might conceivably
// write rather than at what a result carries is a bound in name only: this asserts the
// default is the smaller one.
func TestParseFITAppliesTheDefaultSpanBound(t *testing.T) {
	t.Parallel()

	if api.DefaultMaxFITLaps <= 0 || api.DefaultMaxFITSessions <= 0 {
		t.Fatalf("the default span bounds are %d sessions and %d laps, want positive bounds",
			api.DefaultMaxFITSessions, api.DefaultMaxFITLaps)
	}
	activity, err := api.ParseFITActivity(
		t.Context(), spanFile(20, api.DefaultMaxFITLaps+5), api.FITLimits{})
	if err != nil {
		t.Fatalf("ParseFITActivity() = %v", err)
	}
	if len(activity.Laps) != api.DefaultMaxFITLaps {
		t.Errorf("%d laps, want the default bound of %d",
			len(activity.Laps), api.DefaultMaxFITLaps)
	}
	if !activity.SpansTruncated {
		t.Error("SpansTruncated = false, want the default bound reported")
	}
}

// TestParseFITStopsBeforeItExpandsAnArchiveForACancelledCaller proves the caller's
// context reaches the stage before the decoder.
//
// Expansion is the first thing a hostile file can make expensive, and it happens
// before a single message is decoded. A caller who has already given up must not pay
// for it, and — the part that is easy to get wrong — must not be told its file is
// malformed when the truth is that the call was cancelled: the archive here is
// deliberately not a zip at all, so a check that ran in the wrong order would report
// the malformed archive instead.
func TestParseFITStopsBeforeItExpandsAnArchiveForACancelledCaller(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	broken := append([]byte{'P', 'K', 0x03, 0x04}, []byte("not an archive")...)
	_, err := api.ParseFITActivity(ctx, broken, api.FITLimits{})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("ParseFITActivity() = %v, want context.Canceled", err)
	}
	if errors.Is(err, client.ErrMalformedPayload) {
		t.Error("a cancelled call was reported as a malformed archive")
	}
}

// TestParseFITStopsWhenTheCallerCancels proves the caller's context reaches the
// decode, so an MCP deadline can stop the one part of this package whose cost is set
// by the file rather than by the request.
func TestParseFITStopsWhenTheCallerCancels(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := api.ParseFITActivity(ctx, testkit.FITFile{Samples: rideSamples(60)}.Bytes(),
		api.FITLimits{})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("ParseFITActivity() = %v, want context.Canceled", err)
	}
	if errors.Is(err, client.ErrMalformedPayload) {
		t.Error("a cancelled decode was reported as a malformed file")
	}
}

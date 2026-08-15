package api_test

import (
	"encoding/binary"
	"errors"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// fitContainer wraps a record stream in the FIT header and CRC the format defines.
func fitContainer(body []byte) []byte {
	out := []byte{12, 0x20, 0x5C, 0x08}
	out = binary.LittleEndian.AppendUint32(out, uint32(len(body)))
	out = append(out, ".FIT"...)
	out = append(out, body...)
	return append(out, 0, 0)
}

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

	activity, err := api.ParseFITActivity(fitContainer(body), api.FITLimits{})
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

	activity, err := api.ParseFITActivity(fitContainer(body), api.FITLimits{})
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

	activity, err := api.ParseFITActivity(fitContainer(body), api.FITLimits{})
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

			_, err := api.ParseFITActivity(data, api.FITLimits{})
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

	_, err := api.ParseFITActivity(fitContainer(body), api.FITLimits{})
	if !errors.Is(err, client.ErrMalformedPayload) {
		t.Errorf("ParseFITActivity() = %v, want ErrMalformedPayload", err)
	}
}

// TestParseFITRefusesAnUndefinedMessageSlot proves a data record that names a slot
// no definition filled is refused instead of being read at an arbitrary width.
func TestParseFITRefusesAnUndefinedMessageSlot(t *testing.T) {
	t.Parallel()

	_, err := api.ParseFITActivity(fitContainer([]byte{0x03, 0x00}), api.FITLimits{})
	if !errors.Is(err, client.ErrMalformedPayload) {
		t.Errorf("ParseFITActivity() = %v, want ErrMalformedPayload", err)
	}
}

// TestParseFITRefusesAFileOverItsByteBound is the size bound: a file larger than the
// configured ceiling is refused before it is decoded.
func TestParseFITRefusesAFileOverItsByteBound(t *testing.T) {
	t.Parallel()

	file := testkit.FITFile{Samples: rideSamples(60)}.Bytes()
	_, err := api.ParseFITActivity(file, api.FITLimits{MaxBytes: 32})
	if !errors.Is(err, client.ErrResponseTooLarge) {
		t.Errorf("ParseFITActivity() = %v, want ErrResponseTooLarge", err)
	}
}

// TestParseFITRefusesAFileOverItsMessageBound is the message bound, which is what
// stops a file that is small on the wire but enormous once decoded.
func TestParseFITRefusesAFileOverItsMessageBound(t *testing.T) {
	t.Parallel()

	file := testkit.FITFile{Samples: rideSamples(60)}.Bytes()
	_, err := api.ParseFITActivity(file, api.FITLimits{MaxMessages: 4})
	if !errors.Is(err, client.ErrResponseTooLarge) {
		t.Errorf("ParseFITActivity() = %v, want ErrResponseTooLarge", err)
	}
}

// TestParseFITStopsCollectingAtTheRecordBound proves the retained sample count is
// bounded and that the result says so rather than pretending the ride was short.
func TestParseFITStopsCollectingAtTheRecordBound(t *testing.T) {
	t.Parallel()

	file := testkit.FITFile{Samples: rideSamples(60)}.Bytes()
	activity, err := api.ParseFITActivity(file, api.FITLimits{MaxRecords: 10})
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

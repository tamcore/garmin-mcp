package api

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// A FITNumber is one optional reading. It is a value rather than a pointer because
// an activity carries tens of thousands of records and a pointer per field would be
// an allocation per field.
type FITNumber struct {
	Value float64
	OK    bool
}

// fitNumber returns a present reading.
func fitNumber(value float64) FITNumber { return FITNumber{Value: value, OK: true} }

// A FITRecord is one sample of the record stream.
//
// Coordinates are deliberately absent. The FIT file carries them, and this server
// decodes activity files without ever building a track: a per-second position series
// is the most sensitive thing in the file and no summary here needs it.
type FITRecord struct {
	Time         time.Time
	HeartRate    FITNumber
	Cadence      FITNumber
	Power        FITNumber
	Speed        FITNumber
	Altitude     FITNumber
	Distance     FITNumber
	Grade        FITNumber
	Temperature  FITNumber
	RightBalance FITNumber
	LeftTorque   FITNumber
	RightTorque  FITNumber
	LeftSmooth   FITNumber
	RightSmooth  FITNumber
	LeftPCO      FITNumber
	RightPCO     FITNumber
}

// A FITSpan is one session or lap: the time window it covers and the summary the
// device computed for it.
//
// The summary figures are read from the FIT profile rather than derived from the
// record stream. A device knows its own elapsed time, its own barometric ascent and
// its own averages; a reader that recomputes them from a one-second sample series
// gets a different and worse answer, which is what the record-derived ascent used to
// demonstrate. Every one of these fields is optional: a file that omits one leaves
// the analysis to fall back on the derived value.
type FITSpan struct {
	Start time.Time
	End   time.Time
	Sport string

	Elapsed      FITNumber
	Distance     FITNumber
	Ascent       FITNumber
	Calories     FITNumber
	AvgHeartRate FITNumber
	MaxHeartRate FITNumber
	AvgCadence   FITNumber
	AvgPower     FITNumber
	MaxPower     FITNumber
	NormalizedPw FITNumber
}

// A FITShift is one electronic gear change.
type FITShift struct {
	Time      time.Time
	Front     bool
	FrontGear FITNumber
	RearGear  FITNumber
}

// A FITActivity is the decoded content of one activity file.
type FITActivity struct {
	Sessions []FITSpan
	Laps     []FITSpan
	Records  []FITRecord
	Shifts   []FITShift

	// RecordsTruncated reports that the record stream hit the configured bound and
	// the decode stopped collecting rather than growing without limit.
	RecordsTruncated bool
}

// ParseFITActivity decodes one activity file into the model above.
//
// data is what Garmin served for the original format: a zip archive holding the
// device FIT file, or the bare FIT file. Both are accepted, and both are bounded
// before anything is decoded, so a small archive that expands into a large file is
// refused rather than decoded.
func ParseFITActivity(data []byte, limits FITLimits) (FITActivity, error) {
	resolved := limits.withDefaults()
	raw, err := extractFIT(data, resolved)
	if err != nil {
		return FITActivity{}, err
	}
	return decodeFITActivity(raw, resolved)
}

// zipMagic is the local file header signature of a zip archive.
var zipMagic = []byte{'P', 'K', 0x03, 0x04}

// fitExtension is the archive entry this server unpacks.
const fitExtension = ".fit"

// extractFIT returns the FIT bytes of an archive, or the input when it already is a
// FIT file.
func extractFIT(data []byte, limits FITLimits) ([]byte, error) {
	if int64(len(data)) > limits.MaxBytes {
		return nil, fmt.Errorf("%w: the downloaded activity file is larger than this server decodes",
			client.ErrResponseTooLarge)
	}
	if !bytes.HasPrefix(data, zipMagic) {
		return data, nil
	}

	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("%w: the activity archive could not be opened",
			client.ErrMalformedPayload)
	}
	for _, entry := range archive.File {
		if strings.HasSuffix(strings.ToLower(entry.Name), fitExtension) {
			return readArchiveEntry(entry, limits)
		}
	}
	return nil, fmt.Errorf("%w: the activity archive holds no FIT file",
		client.ErrMalformedPayload)
}

// readArchiveEntry expands one archive entry under the byte bound, so a compression
// bomb is refused at the bound rather than at the allocator.
func readArchiveEntry(entry *zip.File, limits FITLimits) ([]byte, error) {
	reader, err := entry.Open()
	if err != nil {
		return nil, fmt.Errorf("%w: the activity archive entry could not be opened",
			client.ErrMalformedPayload)
	}
	defer func() { _ = reader.Close() }()

	raw, err := io.ReadAll(io.LimitReader(reader, limits.MaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: the activity archive entry could not be read",
			client.ErrMalformedPayload)
	}
	if int64(len(raw)) > limits.MaxBytes {
		return nil, fmt.Errorf("%w: the activity file expands past this server's bound",
			client.ErrResponseTooLarge)
	}
	return raw, nil
}

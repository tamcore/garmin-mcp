package api

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
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
// Coordinates are deliberately absent. The file carries them and the FIT SDK decodes
// every field of every record message, position included, because its decoder has no
// field filter. What this package controls is what it reads out of the SDK's message
// and what it keeps, and that is exactly what is claimed here: no position field is
// read into a FITRecord; the collector's reused message struct is emptied after every
// sample of the two profile position fields *and* of the unknown and developer fields
// a position can otherwise hide in; and no structure this package returns carries one.
// A per-second track is the most sensitive thing in the file and no summary here needs
// it.
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
// descent and its own averages; a reader that recomputes them from a one-second sample series
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
	Descent      FITNumber
	Calories     FITNumber
	AvgHeartRate FITNumber
	MaxHeartRate FITNumber
	AvgCadence   FITNumber
	MaxCadence   FITNumber
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

	// SessionsTruncated and LapsTruncated report the same per span class.
	SessionsTruncated bool
	LapsTruncated     bool
}

// ParseFITActivity decodes one activity file into the model above.
//
// data is what Garmin served for the original format: a zip archive holding the
// device FIT file, or the bare FIT file. Both are accepted, and both are bounded
// before anything is decoded, so a small archive that expands into a large file is
// refused rather than decoded.
//
// ctx bounds the whole call, not only the decode: cancelling it abandons the file
// rather than reading it to its end, which is what lets a caller's deadline reach the
// one part of this package whose cost is set by the file rather than by the request.
// Archive expansion is the first stage that can be made to cost, so the context is
// checked before it starts and again as it proceeds; a cancelled caller is reported as
// itself and never as a malformed file.
func ParseFITActivity(ctx context.Context, data []byte, limits FITLimits) (FITActivity, error) {
	resolved := limits.withDefaults()
	raw, err := extractFIT(ctx, data, resolved)
	if err != nil {
		return FITActivity{}, err
	}
	return decodeFITActivity(ctx, raw, resolved)
}

// zipMagic is the local file header signature of a zip archive.
var zipMagic = []byte{'P', 'K', 0x03, 0x04}

// fitExtension is the archive entry this server unpacks.
const fitExtension = ".fit"

// extractFIT returns the FIT bytes of an archive, or the input when it already is a
// FIT file.
//
// The caller's context is honoured before the archive is opened. Opening and walking a
// zip directory is work a hostile archive sets the size of, and a caller who has
// already given up must not pay for it — nor be told its file is malformed when the
// truth is that the call was cancelled.
func extractFIT(ctx context.Context, data []byte, limits FITLimits) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("reading the activity file: %w", err)
	}
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
			return readArchiveEntry(ctx, entry, limits)
		}
	}
	return nil, fmt.Errorf("%w: the activity archive holds no FIT file",
		client.ErrMalformedPayload)
}

// expansionChunkBytes is how much of an archive entry is expanded between two
// context checks. It is large enough that the checks cost nothing on an ordinary file
// and small enough that a cancelled caller stops promptly on a hostile one.
const expansionChunkBytes = 1 << 20

// readArchiveEntry expands one archive entry under the byte bound and under the
// caller's context, so a compression bomb is refused at the bound rather than at the
// allocator, and a cancelled caller stops paying for the expansion at the next chunk
// rather than at the end of the entry.
func readArchiveEntry(ctx context.Context, entry *zip.File, limits FITLimits) ([]byte, error) {
	reader, err := entry.Open()
	if err != nil {
		return nil, fmt.Errorf("%w: the activity archive entry could not be opened",
			client.ErrMalformedPayload)
	}
	defer func() { _ = reader.Close() }()

	raw, err := expand(ctx, reader, limits.MaxBytes)
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return nil, fmt.Errorf("expanding the activity archive entry: %w", err)
	case err != nil:
		return nil, fmt.Errorf("%w: the activity archive entry could not be read",
			client.ErrMalformedPayload)
	}
	if int64(len(raw)) > limits.MaxBytes {
		return nil, fmt.Errorf("%w: the activity file expands past this server's bound",
			client.ErrResponseTooLarge)
	}
	return raw, nil
}

// expand reads one entry a chunk at a time, checking the context between chunks and
// stopping one byte past the bound so the caller can tell a file at the bound from a
// file over it.
func expand(ctx context.Context, reader io.Reader, limit int64) ([]byte, error) {
	var out bytes.Buffer
	for int64(out.Len()) <= limit {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		switch _, err := io.CopyN(&out, reader, expansionChunkBytes); {
		case errors.Is(err, io.EOF):
			return out.Bytes(), nil
		case err != nil:
			return nil, err
		}
	}
	return out.Bytes(), nil
}

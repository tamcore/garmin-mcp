package api

// FITLimits bounds what one FIT decode may cost. A zero field means the default, so
// FITLimits{} is the bounded configuration rather than an unbounded one.
//
// The bounds stay this server's own now that the container decoding is the FIT
// SDK's. The library decodes whatever it is handed, so refusing an oversized file,
// an oversized message stream and an oversized sample stream is still work this
// package does rather than work it delegates.
type FITLimits struct {
	// MaxBytes bounds the decoded FIT byte stream, archive expansion included.
	MaxBytes int64
	// MaxMessages bounds how many data messages one file may carry.
	MaxMessages int
	// MaxRecords bounds how many per-second records are retained.
	MaxRecords int
	// MaxSpans bounds how many sessions and how many laps are retained, counted
	// separately. It bounds the analysis rather than the result: every span is
	// summarized against the whole record stream, so an unbounded span count over a
	// full sample stream is quadratic work.
	MaxSpans int
}

// Defaults for a FIT decode. Each is the point at which a file stops being a ride
// this server can summarize and starts being a denial of service.
const (
	DefaultMaxFITBytes    int64 = 16 << 20
	DefaultMaxFITMessages       = 500_000
	DefaultMaxFITRecords        = 60_000

	// DefaultMaxFITSpans is far above what a device writes — a recorder stops at a
	// few hundred laps — and far below the count at which summarizing every span
	// against every retained sample stops being affordable.
	DefaultMaxFITSpans = 1000
)

// withDefaults returns a copy of l with every zero field replaced by its default.
func (l FITLimits) withDefaults() FITLimits {
	out := FITLimits{
		MaxBytes:    DefaultMaxFITBytes,
		MaxMessages: DefaultMaxFITMessages,
		MaxRecords:  DefaultMaxFITRecords,
		MaxSpans:    DefaultMaxFITSpans,
	}
	if l.MaxBytes > 0 {
		out.MaxBytes = l.MaxBytes
	}
	if l.MaxMessages > 0 {
		out.MaxMessages = l.MaxMessages
	}
	if l.MaxRecords > 0 {
		out.MaxRecords = l.MaxRecords
	}
	if l.MaxSpans > 0 {
		out.MaxSpans = l.MaxSpans
	}
	return out
}

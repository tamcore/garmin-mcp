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
	// MaxSessions bounds how many sessions are retained.
	MaxSessions int
	// MaxLaps bounds how many laps are retained.
	//
	// The two span classes are bounded separately because they are rendered
	// separately and at very different counts. The bound is on the analysis rather
	// than on the result: every span is summarized against the whole record stream,
	// so the work is the product of the two counts and a span bound applied only when
	// the result is rendered would leave that product unbounded.
	MaxLaps int
}

// Defaults for a FIT decode. Each is the point at which a file stops being a ride
// this server can summarize and starts being a denial of service.
const (
	DefaultMaxFITBytes    int64 = 16 << 20
	DefaultMaxFITMessages       = 500_000
	DefaultMaxFITRecords        = 60_000

	// DefaultMaxFITSessions and DefaultMaxFITLaps are set at what a result actually
	// carries rather than at what a device might conceivably write.
	//
	// A span is summarized against the whole retained record stream, and the stream is
	// bounded at DefaultMaxFITRecords, so every additional span costs another pass over
	// up to 60 000 samples. A file whose spans all overlap — which a hostile file is
	// free to be — therefore turns a generous span bound into tens of millions of
	// record visits per pass, several passes deep. Decoding more spans than any caller
	// can be shown buys nothing and pays for exactly that, so the bound is the render
	// bound: an ordinary ride is far below it, and a file above it is reported
	// truncated rather than summarized in full.
	DefaultMaxFITSessions = 20
	DefaultMaxFITLaps     = 200
)

// withDefaults returns a copy of l with every zero field replaced by its default.
func (l FITLimits) withDefaults() FITLimits {
	out := FITLimits{
		MaxBytes:    DefaultMaxFITBytes,
		MaxMessages: DefaultMaxFITMessages,
		MaxRecords:  DefaultMaxFITRecords,
		MaxSessions: DefaultMaxFITSessions,
		MaxLaps:     DefaultMaxFITLaps,
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
	if l.MaxSessions > 0 {
		out.MaxSessions = l.MaxSessions
	}
	if l.MaxLaps > 0 {
		out.MaxLaps = l.MaxLaps
	}
	return out
}

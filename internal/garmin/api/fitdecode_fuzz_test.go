package api_test

import (
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/api"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// FuzzParseFITActivity exercises the FIT decode path: zip-or-bare-file
// detection, archive extraction and the bounded message decode. Garmin
// activity files are device-written binary data this server never controls,
// so this is the parser in the package most exposed to a malformed or hostile
// file. It performs no I/O and reaches no network: the whole call operates on
// the in-memory byte slice fuzzing provides.
//
// The property is the decoder's own bound: FITLimits{} resolves to the
// package defaults, so whatever ParseFITActivity returns must never carry more
// sessions, laps or records than those defaults, regardless of what the input
// bytes claim.
func FuzzParseFITActivity(f *testing.F) {
	f.Add(testkit.FITContainer([]byte{0x40, 0, 1, 0, 20, 2, 253, 4, 0x86, 7, 2, 0x84}))
	f.Add([]byte{'P', 'K', 0x03, 0x04})
	f.Add([]byte(".FIT"))
	f.Add([]byte{})
	f.Add([]byte{0x00, 0x00, 0x00, 0x00})
	f.Fuzz(func(t *testing.T, data []byte) {
		activity, err := api.ParseFITActivity(t.Context(), data, api.FITLimits{})
		if err != nil {
			return
		}
		if got := len(activity.Records); got > api.DefaultMaxFITRecords {
			t.Fatalf("decoded %d records, over the %d default bound", got, api.DefaultMaxFITRecords)
		}
		if got := len(activity.Sessions); got > api.DefaultMaxFITSessions {
			t.Fatalf("decoded %d sessions, over the %d default bound", got, api.DefaultMaxFITSessions)
		}
		if got := len(activity.Laps); got > api.DefaultMaxFITLaps {
			t.Fatalf("decoded %d laps, over the %d default bound", got, api.DefaultMaxFITLaps)
		}
	})
}

package api

import (
	"bytes"
	"context"
	"fmt"

	"github.com/muktihari/fit/decoder"
	"github.com/muktihari/fit/profile/mesgdef"
	"github.com/muktihari/fit/profile/typedef"
	"github.com/muktihari/fit/proto"
	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// This file drives the FIT SDK's decoder and keeps this server's bounds around it.
//
// The container itself — header, definition and data records, base types, developer
// fields, compressed timestamps, endianness and the checksum — is the library's
// work. What stays here is everything the library has no opinion about: how many
// messages one file may carry, how many samples are retained, which messages are
// collected at all, and how a failure is reported without quoting the file.

// maxFITShifts bounds the collected gear changes. A long ride shifts often, and the
// shift list is a summary input, not a transcript.
const maxFITShifts = 5000

// decodeFITActivity decodes one FIT byte stream into the model under limits.
//
// The decoder broadcasts each message as it is read and retains none of them, so the
// only thing that grows with the file is what fitCollector chooses to keep, and that
// is bounded. Exceeding the message bound cancels the decode rather than letting it
// run to the end of an oversized file.
func decodeFITActivity(raw []byte, limits FITLimits) (FITActivity, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	collector := &fitCollector{limits: limits, stop: cancel}
	dec := decoder.New(bytes.NewReader(raw),
		decoder.WithMesgListener(collector),
		decoder.WithBroadcastOnly())

	for dec.Next() {
		if _, err := dec.DecodeWithContext(ctx); err != nil {
			return FITActivity{}, collector.fail()
		}
	}
	if collector.messages == 0 {
		return FITActivity{}, fmt.Errorf("%w: the activity file carries no records",
			client.ErrMalformedPayload)
	}
	return collector.activity(), nil
}

// fitCollector receives every decoded message and keeps the ones this server reads.
//
// It never returns an error, because the SDK's listener contract has none to return.
// A bound it cannot honour is recorded and the decode is cancelled instead, and the
// recorded reason is what fail turns into the error the caller sees.
type fitCollector struct {
	limits FITLimits
	stop   context.CancelFunc

	// record is reused across messages so a long ride does not allocate one
	// profile struct per sample. Each one is converted before the next is read.
	record mesgdef.Record

	messages  int
	overflow  bool
	truncated bool

	sessions []FITSpan
	laps     []FITSpan
	records  []FITRecord
	shifts   []FITShift
}

// OnMesg receives one decoded message. It is the SDK's listener entry point.
func (c *fitCollector) OnMesg(mesg proto.Message) {
	c.messages++
	if c.messages > c.limits.MaxMessages {
		c.overflow = true
		c.stop()
		return
	}

	switch mesg.Num {
	case typedef.MesgNumRecord:
		c.addRecord(&mesg)
	case typedef.MesgNumSession:
		c.sessions = append(c.sessions, readSession(mesgdef.NewSession(&mesg)))
	case typedef.MesgNumLap:
		c.laps = append(c.laps, readLap(mesgdef.NewLap(&mesg)))
	case typedef.MesgNumEvent:
		c.addShift(&mesg)
	}
}

// addRecord collects one sample, up to the record bound.
func (c *fitCollector) addRecord(mesg *proto.Message) {
	c.record.Reset(mesg)
	if c.record.Timestamp.IsZero() {
		return
	}
	if len(c.records) >= c.limits.MaxRecords {
		c.truncated = true
		return
	}
	c.records = append(c.records, readRecord(&c.record))
}

// addShift collects one electronic gear change, up to the shift bound.
func (c *fitCollector) addShift(mesg *proto.Message) {
	if len(c.shifts) >= maxFITShifts {
		return
	}
	if shift, ok := readShift(mesgdef.NewEvent(mesg)); ok {
		c.shifts = append(c.shifts, shift)
	}
}

// activity returns the collected model.
func (c *fitCollector) activity() FITActivity {
	return FITActivity{
		Sessions:         c.sessions,
		Laps:             c.laps,
		Records:          c.records,
		Shifts:           c.shifts,
		RecordsTruncated: c.truncated,
	}
}

// fail turns a decode failure into a sanitized error. The library's own message
// carries a byte position and would grow to carry more, so none of it is
// reproduced: the caller learns the class of the failure and nothing about the file.
func (c *fitCollector) fail() error {
	if c.overflow {
		return fmt.Errorf("%w: the activity file carries more messages than this server decodes",
			client.ErrResponseTooLarge)
	}
	return fmt.Errorf("%w: the activity file is not a FIT file this server can decode",
		client.ErrMalformedPayload)
}

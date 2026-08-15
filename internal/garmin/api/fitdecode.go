package api

import (
	"encoding/binary"
	"fmt"
	"time"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// FIT container constants.
//
// The FIT container is a public binary format: a 12 or 14 byte header carrying the
// ".FIT" signature and the data size, then a stream of definition and data records,
// then a two-byte CRC. This decoder reads the container only. It carries no Garmin
// FIT profile, so a field it does not name is skipped rather than guessed.
const (
	fitHeaderMinSize = 12
	fitSignatureAt   = 8
	fitSignature     = ".FIT"
	fitDataSizeAt    = 4
	fitCRCSize       = 2

	// Record header bits.
	fitCompressedBit = 0x80
	fitDefinitionBit = 0x40
	fitDeveloperBit  = 0x20
	fitLocalMask     = 0x0F

	// Compressed-timestamp header bits.
	fitCompressedLocalShift = 5
	fitCompressedLocalMask  = 0x03
	fitCompressedTimeMask   = 0x1F
	fitCompressedTimeSpan   = 0x20

	// A definition message addresses one of sixteen local message slots.
	fitLocalSlots = 16

	// A field definition is three bytes: number, size, base type.
	fitFieldDefSize = 3

	// baseTypeMask selects the base type number out of the base type byte.
	baseTypeMask = 0x1F

	// fitDeveloperField marks a definition entry this decoder steps over.
	fitDeveloperField = 0xFF
)

// FIT base type numbers.
const (
	baseEnum    = 0x00
	baseSint8   = 0x01
	baseUint8   = 0x02
	baseSint16  = 0x03
	baseUint16  = 0x04
	baseSint32  = 0x05
	baseUint32  = 0x06
	baseString  = 0x07
	baseFloat32 = 0x08
	baseFloat64 = 0x09
	baseUint8z  = 0x0A
	baseUint16z = 0x0B
	baseUint32z = 0x0C
	baseByte    = 0x0D
	baseSint64  = 0x0E
	baseUint64  = 0x0F
	baseUint64z = 0x10
)

// fitEpoch is the FIT date_time epoch, 1989-12-31T00:00:00Z.
var fitEpoch = time.Date(1989, time.December, 31, 0, 0, 0, 0, time.UTC)

// fitTime renders a FIT date_time as an instant.
func fitTime(seconds uint64) time.Time {
	return fitEpoch.Add(time.Duration(seconds) * time.Second)
}

// FITLimits bounds what one FIT decode may cost. A zero field means the default, so
// FITLimits{} is the bounded configuration rather than an unbounded one.
type FITLimits struct {
	// MaxBytes bounds the decoded FIT byte stream, archive expansion included.
	MaxBytes int64
	// MaxMessages bounds how many data messages one file may carry.
	MaxMessages int
	// MaxRecords bounds how many per-second records are retained.
	MaxRecords int
}

// Defaults for a FIT decode. Each is the point at which a file stops being a ride
// this server can summarize and starts being a denial of service.
const (
	DefaultMaxFITBytes    int64 = 16 << 20
	DefaultMaxFITMessages       = 500_000
	DefaultMaxFITRecords        = 60_000
)

// withDefaults returns a copy of l with every zero field replaced by its default.
func (l FITLimits) withDefaults() FITLimits {
	out := FITLimits{
		MaxBytes:    DefaultMaxFITBytes,
		MaxMessages: DefaultMaxFITMessages,
		MaxRecords:  DefaultMaxFITRecords,
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
	return out
}

// A fitFieldDef is one field of a definition message.
type fitFieldDef struct {
	num  uint8
	size int
	base uint8
}

// A fitDefinition describes the layout of every data message in one local slot.
type fitDefinition struct {
	global uint16
	little bool
	fields []fitFieldDef
	size   int
}

// fitVisitor is called once per decoded data message.
type fitVisitor func(global uint16, fields fitFields) error

// decodeFIT walks the record stream of a FIT file and calls visit for each data
// message. It is tolerant by design: an unknown global message number, an unknown
// field and a developer field are all skipped, because Garmin's files carry more
// than this server names and a new message must not fail a whole ride.
func decodeFIT(data []byte, limits FITLimits, visit fitVisitor) error {
	resolved := limits.withDefaults()
	body, err := fitBody(data, resolved)
	if err != nil {
		return err
	}
	dec := &fitDecoder{data: body, limits: resolved, visit: visit}
	return dec.run()
}

// fitBody validates the header and returns the record stream it announces.
func fitBody(data []byte, limits FITLimits) ([]byte, error) {
	if int64(len(data)) > limits.MaxBytes {
		return nil, fmt.Errorf("%w: the activity file is larger than this server decodes",
			client.ErrResponseTooLarge)
	}
	if len(data) < fitHeaderMinSize+fitCRCSize {
		return nil, fmt.Errorf("%w: the activity file is too short to be a FIT file",
			client.ErrMalformedPayload)
	}
	if string(data[fitSignatureAt:fitSignatureAt+len(fitSignature)]) != fitSignature {
		return nil, fmt.Errorf("%w: the activity file carries no FIT signature",
			client.ErrMalformedPayload)
	}

	headerSize := int(data[0])
	if headerSize < fitHeaderMinSize || headerSize > len(data) {
		return nil, fmt.Errorf("%w: the FIT header size is out of range", client.ErrMalformedPayload)
	}
	end := min(headerSize+int(binary.LittleEndian.Uint32(data[fitDataSizeAt:])),
		// Garmin has been observed serving a truncated announced size. Decoding
		// what is present is more useful than refusing the whole ride.
		len(data)-fitCRCSize)
	if end <= headerSize {
		return nil, fmt.Errorf("%w: the FIT file carries no records", client.ErrMalformedPayload)
	}
	return data[headerSize:end], nil
}

// fitDecoder holds the decode state of one file.
type fitDecoder struct {
	data     []byte
	pos      int
	defs     [fitLocalSlots]*fitDefinition
	stamp    uint64
	hasStamp bool
	messages int
	limits   FITLimits
	visit    fitVisitor
	scratch  fitFields
}

// run walks every record of the stream.
func (d *fitDecoder) run() error {
	for d.pos < len(d.data) {
		header := d.data[d.pos]
		d.pos++

		var err error
		switch {
		case header&fitCompressedBit != 0:
			err = d.readCompressed(header)
		case header&fitDefinitionBit != 0:
			err = d.readDefinition(header)
		default:
			err = d.readData(int(header&fitLocalMask), nil)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// readDefinition installs the layout of one local slot.
func (d *fitDecoder) readDefinition(header byte) error {
	const fixed = 5 // reserved, architecture, global number, field count
	if d.pos+fixed > len(d.data) {
		return fitTruncated()
	}

	little := d.data[d.pos+1] == 0
	global := fitOrder(little).Uint16(d.data[d.pos+2:])
	count := int(d.data[d.pos+4])
	d.pos += fixed

	def := &fitDefinition{global: global, little: little, fields: make([]fitFieldDef, 0, count)}
	if err := d.readFieldDefs(def, count); err != nil {
		return err
	}
	if header&fitDeveloperBit != 0 {
		if err := d.readDeveloperDefs(def); err != nil {
			return err
		}
	}
	d.defs[header&fitLocalMask] = def
	return nil
}

// readFieldDefs reads count field definitions into def.
func (d *fitDecoder) readFieldDefs(def *fitDefinition, count int) error {
	if d.pos+count*fitFieldDefSize > len(d.data) {
		return fitTruncated()
	}
	for range count {
		field := fitFieldDef{
			num:  d.data[d.pos],
			size: int(d.data[d.pos+1]),
			base: d.data[d.pos+2],
		}
		d.pos += fitFieldDefSize
		def.fields = append(def.fields, field)
		def.size += field.size
	}
	return nil
}

// readDeveloperDefs records the size of each developer field so its bytes can be
// stepped over. Developer data is application-defined and is never interpreted.
func (d *fitDecoder) readDeveloperDefs(def *fitDefinition) error {
	if d.pos >= len(d.data) {
		return fitTruncated()
	}
	count := int(d.data[d.pos])
	d.pos++
	if d.pos+count*fitFieldDefSize > len(d.data) {
		return fitTruncated()
	}
	for range count {
		size := int(d.data[d.pos+1])
		d.pos += fitFieldDefSize
		def.fields = append(def.fields,
			fitFieldDef{num: fitDeveloperField, size: size, base: baseByte})
		def.size += size
	}
	return nil
}

// readCompressed reads a data message whose header carries a five-bit timestamp
// offset instead of a full timestamp field.
func (d *fitDecoder) readCompressed(header byte) error {
	local := int((header >> fitCompressedLocalShift) & fitCompressedLocalMask)
	offset := uint64(header & fitCompressedTimeMask)
	if !d.hasStamp {
		return d.readData(local, nil)
	}

	previous := d.stamp & fitCompressedTimeMask
	stamp := d.stamp - previous + offset
	if offset < previous {
		stamp += fitCompressedTimeSpan
	}
	d.stamp = stamp
	return d.readData(local, &stamp)
}

// readData decodes one data message and hands it to the visitor.
func (d *fitDecoder) readData(local int, stamp *uint64) error {
	def := d.defs[local]
	if def == nil {
		return fmt.Errorf("%w: a FIT data record names an undefined message slot",
			client.ErrMalformedPayload)
	}
	if d.pos+def.size > len(d.data) {
		return fitTruncated()
	}
	d.messages++
	if d.messages > d.limits.MaxMessages {
		return fmt.Errorf("%w: the activity file carries more messages than this server decodes",
			client.ErrResponseTooLarge)
	}

	d.scratch = d.decodeFields(def, stamp)
	return d.visit(def.global, d.scratch)
}

// fieldTimestamp is the universal timestamp field number.
const fieldTimestamp = 253

// decodeFields decodes one message body into the reused scratch slice.
func (d *fitDecoder) decodeFields(def *fitDefinition, stamp *uint64) fitFields {
	fields := d.scratch[:0]
	if stamp != nil {
		fields = append(fields, fitField{
			num:   fieldTimestamp,
			value: fitValue{number: float64(*stamp), bits: *stamp, valid: true},
		})
	}

	for _, field := range def.fields {
		raw := d.data[d.pos : d.pos+field.size]
		d.pos += field.size
		if field.num == fitDeveloperField && field.base == baseByte {
			continue
		}
		value := fitDecodeValue(field.base, raw, def.little)
		if field.num == fieldTimestamp && value.valid {
			d.stamp, d.hasStamp = value.bits, true
		}
		fields = append(fields, fitField{num: field.num, value: value})
	}
	return fields
}

func fitTruncated() error {
	return fmt.Errorf("%w: the activity file ends inside a record", client.ErrMalformedPayload)
}

// fitOrder returns the byte order a definition announced.
func fitOrder(little bool) binary.ByteOrder {
	if little {
		return binary.LittleEndian
	}
	return binary.BigEndian
}

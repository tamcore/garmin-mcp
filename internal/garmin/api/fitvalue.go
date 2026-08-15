package api

import (
	"encoding/binary"
	"math"
	"time"
)

// This file is the value layer of the FIT decoder: the base types the container
// defines, the sentinel each one reserves for a missing reading, and the accessors
// a consumer reads one decoded message through.

// A fitValue is one decoded field value. An invalid value — the per-type sentinel
// the format reserves — is reported as not valid rather than as a number, so a
// missing sensor never becomes a zero reading.
type fitValue struct {
	number float64
	bits   uint64
	text   string
	isText bool
	valid  bool
}

// A fitField is one decoded field of one message.
type fitField struct {
	num   uint8
	value fitValue
}

// fitFields is the decoded field set of one message.
//
// It is valid only for the duration of the visit call it is passed to: the decoder
// reuses the backing array. A consumer copies what it needs.
type fitFields []fitField

// find returns the value of one field number.
func (f fitFields) find(num uint8) (fitValue, bool) {
	for _, field := range f {
		if field.num == num && field.value.valid {
			return field.value, true
		}
	}
	return fitValue{}, false
}

// number returns a numeric field.
func (f fitFields) number(num uint8) (float64, bool) {
	value, ok := f.find(num)
	if !ok || value.isText {
		return 0, false
	}
	return value.number, true
}

// unsigned returns the raw integer bits of a field, which is how a packed bit field
// such as a gear-change payload is read.
func (f fitFields) unsigned(num uint8) (uint64, bool) {
	value, ok := f.find(num)
	if !ok || value.isText {
		return 0, false
	}
	return value.bits, true
}

// timestamp returns a date_time field as an instant.
func (f fitFields) timestamp(num uint8) (time.Time, bool) {
	seconds, ok := f.unsigned(num)
	if !ok {
		return time.Time{}, false
	}
	return fitTime(seconds), true
}

// fitBaseSize is the width of one element of a base type. Zero means the base type
// is not one the format defines, which makes the field opaque.
func fitBaseSize(base uint8) int {
	switch base & baseTypeMask {
	case baseEnum, baseSint8, baseUint8, baseString, baseUint8z, baseByte:
		return 1
	case baseSint16, baseUint16, baseUint16z:
		return 2
	case baseSint32, baseUint32, baseFloat32, baseUint32z:
		return 4
	case baseFloat64, baseSint64, baseUint64, baseUint64z:
		return 8
	}
	return 0
}

// fitDecodeValue decodes the first element of one field.
//
// A field wider than its base type is an array. Only the first element is decoded:
// every field this server reads is scalar, and keeping arrays out of the value model
// keeps the model small.
func fitDecodeValue(base uint8, raw []byte, little bool) fitValue {
	if base&baseTypeMask == baseString {
		return fitDecodeText(raw)
	}
	width := fitBaseSize(base)
	if width == 0 || len(raw) < width {
		return fitValue{}
	}
	return fitDecodeNumber(base, raw[:width], fitOrder(little))
}

// fitDecodeText decodes a UTF-8 string field, which the format terminates with NUL.
func fitDecodeText(raw []byte) fitValue {
	end := len(raw)
	for index, b := range raw {
		if b == 0 {
			end = index
			break
		}
	}
	text := string(raw[:end])
	return fitValue{text: text, isText: true, valid: text != ""}
}

// fitDecodeNumber decodes one numeric element.
func fitDecodeNumber(base uint8, raw []byte, order binary.ByteOrder) fitValue {
	switch base & baseTypeMask {
	case baseEnum, baseUint8, baseByte:
		return fitUnsigned(uint64(raw[0]), math.MaxUint8)
	case baseUint8z:
		return fitUnsignedZ(uint64(raw[0]))
	case baseSint8:
		return fitSigned(int64(int8(raw[0])), math.MaxInt8)
	case baseUint16:
		return fitUnsigned(uint64(order.Uint16(raw)), math.MaxUint16)
	case baseUint16z:
		return fitUnsignedZ(uint64(order.Uint16(raw)))
	case baseSint16:
		return fitSigned(int64(int16(order.Uint16(raw))), math.MaxInt16)
	case baseUint32:
		return fitUnsigned(uint64(order.Uint32(raw)), math.MaxUint32)
	case baseUint32z:
		return fitUnsignedZ(uint64(order.Uint32(raw)))
	case baseSint32:
		return fitSigned(int64(int32(order.Uint32(raw))), math.MaxInt32)
	default:
		return fitDecodeWide(base, raw, order)
	}
}

// fitDecodeWide decodes the eight-byte base types and the floats.
func fitDecodeWide(base uint8, raw []byte, order binary.ByteOrder) fitValue {
	switch base & baseTypeMask {
	case baseFloat32:
		bits := order.Uint32(raw)
		return fitFloat(float64(math.Float32frombits(bits)), bits == math.MaxUint32)
	case baseFloat64:
		bits := order.Uint64(raw)
		return fitFloat(math.Float64frombits(bits), bits == math.MaxUint64)
	case baseUint64:
		return fitUnsigned(order.Uint64(raw), math.MaxUint64)
	case baseUint64z:
		return fitUnsignedZ(order.Uint64(raw))
	case baseSint64:
		return fitSigned(int64(order.Uint64(raw)), math.MaxInt64)
	}
	return fitValue{}
}

// fitUnsigned reports an unsigned value unless it is the type's invalid sentinel.
func fitUnsigned(value, invalid uint64) fitValue {
	if value == invalid {
		return fitValue{}
	}
	return fitValue{number: float64(value), bits: value, valid: true}
}

// fitUnsignedZ reports a z-typed value, whose invalid sentinel is zero.
func fitUnsignedZ(value uint64) fitValue {
	if value == 0 {
		return fitValue{}
	}
	return fitValue{number: float64(value), bits: value, valid: true}
}

// fitSigned reports a signed value unless it is the type's invalid sentinel.
func fitSigned(value, invalid int64) fitValue {
	if value == invalid {
		return fitValue{}
	}
	return fitValue{number: float64(value), bits: uint64(value), valid: true}
}

// fitFloat reports a float value unless it is invalid or not a number.
func fitFloat(value float64, invalid bool) fitValue {
	if invalid || math.IsNaN(value) || math.IsInf(value, 0) {
		return fitValue{}
	}
	return fitValue{number: value, bits: uint64(value), valid: true}
}

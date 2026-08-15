package testkit

import (
	"encoding/binary"
	"math"
)

// This file is the byte layer of the synthetic FIT builder: the container the
// records are wrapped in, the checksum that container ends with, and the optional
// field writers the message builders are composed from.

// FIT container constants.
const (
	fitHeaderSize = 12
	fitProtocol   = 0x20
	fitProfile    = 2140
	fitCRCSize    = 2
)

// The invalid sentinels of the base types the builder emits.
const (
	invalidUint8  = 0xFF
	invalidSint8  = 0x7F
	invalidUint16 = 0xFFFF
	invalidSint16 = 0x7FFF
	invalidUint32 = 0xFFFFFFFF
)

// crcNibbles is the sixteen-entry table of the FIT checksum, which the format
// defines over the whole file up to but excluding the trailing checksum itself.
var crcNibbles = [16]uint16{
	0x0000, 0xCC01, 0xD801, 0x1400, 0xF001, 0x3C00, 0x2800, 0xE401,
	0xA001, 0x6C00, 0x7800, 0xB401, 0x5000, 0x9C01, 0x8801, 0x4400,
}

// The bit widths the checksum folds one nibble at a time.
const (
	crcLowMask   = 0x0FFF
	crcNibbleBit = 0x0F
	crcShift     = 4
)

// fitCRC returns the FIT checksum of data.
func fitCRC(data []byte) uint16 {
	var crc uint16
	for _, b := range data {
		crc = crcNibble(crc, b&crcNibbleBit)
		crc = crcNibble(crc, (b>>crcShift)&crcNibbleBit)
	}
	return crc
}

// crcNibble folds one nibble into the running checksum.
func crcNibble(crc uint16, nibble byte) uint16 {
	carry := crcNibbles[crc&crcNibbleBit]
	crc = (crc >> crcShift) & crcLowMask
	return crc ^ carry ^ crcNibbles[nibble]
}

// FITContainer wraps a record stream in the twelve-byte header and the checksum the
// FIT container defines, so a synthetic file is a valid one rather than one that
// only a lenient reader accepts.
func FITContainer(body []byte) []byte {
	out := make([]byte, 0, fitHeaderSize+len(body)+fitCRCSize)
	out = append(out, fitHeaderSize, fitProtocol)
	out = binary.LittleEndian.AppendUint16(out, fitProfile)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(body)))
	out = append(out, ".FIT"...)
	out = append(out, body...)
	return binary.LittleEndian.AppendUint16(out, fitCRC(out))
}

// semicirclesPerDegree converts degrees into the unit FIT stores a position in.
const semicirclesPerDegree = 1 << 31 / 180.0

// invalidSint32 is the sentinel of the base type a position is written as.
const invalidSint32 = 0x7FFFFFFF

// appendSemicircles writes an optional position component in semicircles.
func appendSemicircles(out []byte, degrees *float64) []byte {
	if degrees == nil {
		return binary.LittleEndian.AppendUint32(out, invalidSint32)
	}
	return binary.LittleEndian.AppendUint32(out,
		uint32(int32(math.Round(*degrees*semicirclesPerDegree))))
}

// appendUint8 writes an optional byte reading, or its invalid sentinel.
func appendUint8(out []byte, value *int) []byte {
	if value == nil {
		return append(out, invalidUint8)
	}
	return append(out, byte(*value))
}

// appendSint8 writes an optional signed byte reading.
func appendSint8(out []byte, value *int) []byte {
	if value == nil {
		return append(out, invalidSint8)
	}
	return append(out, byte(int8(*value)))
}

// appendScaledUint8 writes an optional scaled byte reading.
func appendScaledUint8(out []byte, value *float64, scale float64) []byte {
	if value == nil {
		return append(out, invalidUint8)
	}
	return append(out, byte(math.Round(*value*scale)))
}

// appendUint16 writes an optional two-byte reading.
func appendUint16(out []byte, value *int) []byte {
	if value == nil {
		return binary.LittleEndian.AppendUint16(out, invalidUint16)
	}
	return binary.LittleEndian.AppendUint16(out, uint16(*value))
}

// appendScaledUint16 writes an optional scaled and offset two-byte reading.
func appendScaledUint16(out []byte, value *float64, scale, offset float64) []byte {
	if value == nil {
		return binary.LittleEndian.AppendUint16(out, invalidUint16)
	}
	return binary.LittleEndian.AppendUint16(out, uint16(math.Round((*value+offset)*scale)))
}

// appendScaledSint16 writes an optional scaled signed two-byte reading.
func appendScaledSint16(out []byte, value *float64, scale float64) []byte {
	if value == nil {
		return binary.LittleEndian.AppendUint16(out, invalidSint16)
	}
	return binary.LittleEndian.AppendUint16(out, uint16(int16(math.Round(*value*scale))))
}

// appendScaledUint32 writes an optional scaled four-byte reading.
func appendScaledUint32(out []byte, value *float64, scale float64) []byte {
	if value == nil {
		return binary.LittleEndian.AppendUint32(out, invalidUint32)
	}
	return binary.LittleEndian.AppendUint32(out, uint32(math.Round(*value*scale)))
}

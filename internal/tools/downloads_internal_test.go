package tools

import (
	"errors"
	"testing"
)

// TestBoundedSinkAbortsTheTransferAtItsBound proves the bound is enforced rather
// than merely observed.
//
// A sink that swallowed the overflow and reported success would let the rest of an
// oversized file be transferred and discarded, so the refusal has to reach the copy.
// Write therefore reports the refusal itself, and reports it for every later chunk
// too, so a copier that ignores one error still cannot fill the buffer.
func TestBoundedSinkAbortsTheTransferAtItsBound(t *testing.T) {
	t.Parallel()

	const limit = 8
	sink := newBoundedSink(limit)

	if written, err := sink.Write(make([]byte, limit)); written != limit || err != nil {
		t.Fatalf("Write() at the bound = %d, %v, want %d and no error", written, err, limit)
	}

	written, err := sink.Write([]byte{1})
	if written != 0 {
		t.Errorf("Write() past the bound accepted %d bytes, want none", written)
	}
	if !errors.Is(err, ErrResultTooLarge) {
		t.Fatalf("Write() past the bound = %v, want ErrResultTooLarge", err)
	}

	if _, err := sink.Write([]byte{2}); !errors.Is(err, ErrResultTooLarge) {
		t.Errorf("a later Write() = %v, want the same refusal", err)
	}
	if sink.len() != 0 {
		t.Errorf("the sink kept %d bytes of a refused transfer, want none", sink.len())
	}
	if !errors.Is(sink.err(), ErrResultTooLarge) {
		t.Errorf("err() = %v, want ErrResultTooLarge", sink.err())
	}
}

package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

// Store-level re-sealing, the FileStore half of ADR 0005's second open item.
//
// FileStore is single-principal by construction: a local deployment binds exactly
// one account (internal/identity.NewStdioResolver), so unlike the SQLite backend
// there is no directory to enumerate blindly. The record file name is a SHA-256
// digest of the principal id, which is one-way on purpose, so a caller must already
// know the principal id to open its record at all — the same requirement every
// other method on this type already has. Reseal takes that principal id explicitly
// rather than scanning the records directory.
//
// Concurrency within one process is the per-principal mutex Save and Load already
// use, so a reseal and a concurrent Save from the SAME *FileStore for the same
// principal cannot interleave: whichever acquires the lock first commits, and the
// other proceeds against whatever that left behind. That mutex is store state, not
// package state, so it covers nothing across two separate *FileStore instances —
// which is exactly the shape `rotate-key` and a live `serve` process actually have:
// two processes, each opening its own *FileStore over the same directory, sharing
// no lock at all. Closing that gap needs two things together, not either alone:
// the OS-level advisory lock in filelock_unix.go makes the whole read-modify-write
// section run exclusively of the other process's, and a content-equality re-check
// immediately before the write is the same defense-in-depth the SQLite backend's
// principals and schema_meta tables already apply for rows with no separate
// version counter. The lock is what actually closes the window: a Go-level "read,
// then write" is two separate operations and stays racy on its own, unlike a
// single SQL UPDATE...WHERE, which is atomic at the database engine.

// ResealOutcome distinguishes why Reseal did or did not rewrite a record. A
// plain bool cannot: "nothing needed resealing" and "another writer moved the
// record out from under this attempt" are different situations for a caller
// deciding whether it is safe to say the record is at the active key, and
// collapsing them into one false was exactly the defect this type closes —
// see the MEDIUM item on the lost-race report in AGENTS.md's fix list.
type ResealOutcome int

const (
	// ResealNoRecord means the principal has no stored record at all.
	ResealNoRecord ResealOutcome = iota
	// ResealAlreadyCurrent means the record was already sealed under the
	// active key; nothing was written.
	ResealAlreadyCurrent
	// ResealRewrote means the record was re-sealed onto the active key.
	ResealRewrote
	// ResealRaced means the record needed resealing, but another writer
	// changed it between this attempt's read and its write, so nothing was
	// written here. The record's key version as of THIS call is unknown: a
	// caller must not report it as being at the active key.
	ResealRaced
)

// Reseal re-encrypts principal's record onto the active key when it is not
// already there, and reports which of ResealOutcome's cases happened.
//
// ErrNoTokens is not surfaced as a failure: a principal with no stored record has
// nothing to reseal, which is the state every principal starts in. Calling this
// again after it already reported ResealAlreadyCurrent is a no-op, which is what
// makes a killed rotation resumable: the next run re-reads the same record, sees
// it is already sealed under the active key, and changes nothing.
func (s *FileStore) Reseal(ctx context.Context, principal string) (ResealOutcome, error) {
	if err := checkRequest(ctx, principal); err != nil {
		return ResealNoRecord, err
	}

	unlock := s.locks.lock(principal)
	defer unlock()

	crossLock, err := lockRecord(ctx, s.lockPath(principal))
	if err != nil {
		return ResealNoRecord, fmt.Errorf("store: lock record for reseal: %w", err)
	}
	defer func() { _ = crossLock.release() }()

	record, err := s.readRecord(principal)
	switch {
	case errors.Is(err, ErrNoTokens):
		return ResealNoRecord, nil
	case err != nil:
		return ResealNoRecord, err
	}

	sealed, err := base64.StdEncoding.DecodeString(record.Payload)
	if err != nil {
		return ResealNoRecord, fmt.Errorf("store: record payload is not base64: %w", ErrCorruptRecord)
	}

	plan, err := s.crypt.planReseal(principal, recordAAD(record.Schema, record.Version), sealed)
	if err != nil {
		return ResealNoRecord, fmt.Errorf("store: reseal record: %w: %w", ErrCorruptRecord, err)
	}
	if !plan.changed {
		return ResealAlreadyCurrent, nil
	}

	content, err := json.Marshal(storedRecord{
		Schema:  record.Schema,
		Version: record.Version,
		Payload: base64.StdEncoding.EncodeToString(plan.sealed),
	})
	if err != nil {
		return ResealNoRecord, fmt.Errorf("store: encode resealed record: %w", err)
	}

	// Re-read immediately before writing: the per-process mutex above only
	// serializes goroutines inside THIS *FileStore. A second process — a live
	// serve committing a concurrent refresh — can commit between the read at
	// the top of this method and this point, and that write must never be
	// clobbered. Refuse to write unless the record on disk is still exactly
	// what the plan above was computed from; a mismatch means a concurrent
	// write already happened, and the record it left behind is picked up by a
	// later reseal instead.
	current, err := s.readRecord(principal)
	if err != nil {
		return ResealNoRecord, err
	}
	if current.Version != record.Version || current.Payload != record.Payload {
		return ResealRaced, nil
	}

	if err := writeFileAtomically(s.recordPath(principal), content); err != nil {
		return ResealNoRecord, err
	}
	return ResealRewrote, nil
}

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

// Reseal re-encrypts principal's record onto the active key when it is not
// already there, and reports whether a rewrite happened.
//
// ErrNoTokens is not surfaced as a failure: a principal with no stored record has
// nothing to reseal, which is the state every principal starts in. Calling this
// again after it already reported changed=false is a no-op, which is what makes a
// killed rotation resumable: the next run re-reads the same record, sees it is
// already sealed under the active key, and changes nothing.
func (s *FileStore) Reseal(ctx context.Context, principal string) (bool, error) {
	if err := checkRequest(ctx, principal); err != nil {
		return false, err
	}

	unlock := s.locks.lock(principal)
	defer unlock()

	crossLock, err := lockRecord(s.lockPath(principal))
	if err != nil {
		return false, fmt.Errorf("store: lock record for reseal: %w", err)
	}
	defer func() { _ = crossLock.release() }()

	record, err := s.readRecord(principal)
	switch {
	case errors.Is(err, ErrNoTokens):
		return false, nil
	case err != nil:
		return false, err
	}

	sealed, err := base64.StdEncoding.DecodeString(record.Payload)
	if err != nil {
		return false, fmt.Errorf("store: record payload is not base64: %w", ErrCorruptRecord)
	}

	plan, err := s.crypt.planReseal(principal, recordAAD(record.Schema, record.Version), sealed)
	if err != nil {
		return false, fmt.Errorf("store: reseal record: %w: %w", ErrCorruptRecord, err)
	}
	if !plan.changed {
		return false, nil
	}

	content, err := json.Marshal(storedRecord{
		Schema:  record.Schema,
		Version: record.Version,
		Payload: base64.StdEncoding.EncodeToString(plan.sealed),
	})
	if err != nil {
		return false, fmt.Errorf("store: encode resealed record: %w", err)
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
		return false, err
	}
	if current.Version != record.Version || current.Payload != record.Payload {
		return false, nil
	}

	if err := writeFileAtomically(s.recordPath(principal), content); err != nil {
		return false, err
	}
	return true, nil
}

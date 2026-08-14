package store

import (
	"errors"
	"fmt"

	"github.com/tamcore/garmin-mcp/internal/securefile"
)

// Every filesystem operation in this package goes through internal/securefile,
// which owns the hardening: paths are walked component by component against
// directory file descriptors, a read requires a regular owner-only file and never
// blocks, and a write lands atomically. This file is only the translation of that
// package's sentinels onto this package's, so callers keep testing for ErrNoTokens,
// ErrInsecurePath and ErrInsecurePermissions.
//
// Source: Client.dump and Client.load in client.py (0.3.10), which write 0o600
// inside 0o700 and chmod both unconditionally (GHSA-wjhr-76vg-2hvc).

// File and directory modes for token material. Both are enforced with an explicit
// chmod after creation, because the modes passed to mkdir and open are masked by
// the process umask, which is global and may be hostile.
const (
	tokenFileMode = 0o600
	tokenDirMode  = 0o700
)

// maxRecordBytes bounds a record read. A token record is a few kilobytes; anything
// larger is a mistake or an attempt to exhaust memory.
const maxRecordBytes = 64 << 10

// ensureOwnerOnlyDir creates dir and every missing parent, then enforces owner-only
// access on dir itself.
func ensureOwnerOnlyDir(dir string) error {
	return translate("prepare directory", dir, securefile.EnsureDir(dir, tokenDirMode), ErrInsecurePath)
}

// readOwnerOnlyFile reads path, refusing anything that is not a regular owner-only
// file reached without following a symlink. An absent file reports notFound, so the
// caller maps it onto its own sentinel.
func readOwnerOnlyFile(path string, notFound error) ([]byte, error) {
	raw, err := securefile.ReadFile(path, maxRecordBytes)
	if err != nil {
		return nil, translate("read", path, err, notFound)
	}
	return raw, nil
}

// writeFileAtomically writes content to path so a concurrent reader sees either the
// previous record or the new one, never a truncated file.
func writeFileAtomically(path string, content []byte) error {
	err := securefile.WriteFile(path, content, tokenFileMode)
	return translate("write", path, err, ErrInsecurePath)
}

// removeFile deletes path. An absent path is not an error.
func removeFile(path string) error {
	return translate("remove", path, securefile.Remove(path), ErrInsecurePath)
}

// checkPathAncestry rejects a configured path whose ancestry contains a symlink. It
// is a start-up check that reports a misconfiguration once, before anything is
// written; the operations above do not rely on it.
func checkPathAncestry(path string) error {
	return translate("check", path, securefile.CheckAncestry(path), ErrInsecurePath)
}

// translate maps a securefile failure onto this package's sentinels. notFound is
// what an absent path reports, which differs per caller: a missing record means
// ErrNoTokens, while a missing directory in a path that must exist is a path
// problem.
func translate(operation, path string, err error, notFound error) error {
	if err == nil {
		return nil
	}

	switch {
	case errors.Is(err, securefile.ErrNotFound):
		return fmt.Errorf("store: %s %q: %w", operation, path, notFound)
	case errors.Is(err, securefile.ErrInsecurePermissions):
		return fmt.Errorf("store: %s %q: %w: %w", operation, path, ErrInsecurePermissions, err)
	case errors.Is(err, securefile.ErrInsecurePath):
		return fmt.Errorf("store: %s %q: %w: %w", operation, path, ErrInsecurePath, err)
	}
	return fmt.Errorf("store: %s %q: %w", operation, path, err)
}

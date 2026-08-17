//go:build unix

package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/securefile"
)

// TestLockRecordRefusesASymlinkedLockFile is the hardening property item 2
// requires: lockRecord must never follow a symlink planted at the lock path.
// Following it would let a planted symlink aimed at the record file itself
// split the lock domain across two inodes once the record is atomically
// replaced, so two processes could each believe they hold the lock and write
// concurrently — exactly the failure the lock exists to prevent.
func TestLockRecordRefusesASymlinkedLockFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "record.json")
	if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
		t.Fatalf("seed target file: %v", err)
	}

	lockPath := filepath.Join(dir, "record.json.lock")
	if err := os.Symlink(target, lockPath); err != nil {
		t.Fatalf("plant symlinked lock file: %v", err)
	}

	if _, err := lockRecord(context.Background(), lockPath); err == nil {
		t.Fatal("lockRecord followed a symlinked lock path, want it refused")
	} else if !errors.Is(err, securefile.ErrInsecurePath) {
		t.Fatalf("lockRecord on a symlinked lock path: err = %v, want it to wrap securefile.ErrInsecurePath", err)
	}
}

// TestLockRecordReturnsPromptlyForAnAlreadyCancelledContext is half of the
// MEDIUM item: flock must honour the caller's context rather than blocking
// indefinitely on unix.LOCK_EX. A context that is already done must be
// refused up front, before ever touching the kernel lock call.
func TestLockRecordReturnsPromptlyForAnAlreadyCancelledContext(t *testing.T) {
	dir := tempDir(t)
	lockPath := filepath.Join(dir, "record.json.lock")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := lockRecord(ctx, lockPath); !errors.Is(err, context.Canceled) {
		t.Fatalf("lockRecord with an already-cancelled context: err = %v, want it to wrap context.Canceled", err)
	}
}

// TestLockRecordReturnsPromptlyWhenCancelledWhileContended is the other half:
// a context cancelled WHILE another descriptor holds the lock must make a
// blocked lockRecord return promptly with a wrapped context error, not hang
// until the other holder releases (which, for a stopped or wedged process,
// may be never).
func TestLockRecordReturnsPromptlyWhenCancelledWhileContended(t *testing.T) {
	dir := tempDir(t)
	lockPath := filepath.Join(dir, "record.json.lock")

	held, err := lockRecord(context.Background(), lockPath)
	if err != nil {
		t.Fatalf("take the record lock: %v", err)
	}
	defer func() { _ = held.release() }()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// The call runs in a goroutine and the result arrives on a channel, rather
	// than being awaited inline, so that a build which ignores ctx and blocks in
	// LOCK_EX fails this test in seconds. Called inline it would instead block
	// forever inside flock(2) — uninterruptible from Go — and the regression
	// would surface only as the test binary's own timeout panic ten minutes
	// later, which reads like infrastructure trouble rather than the assertion
	// it actually is.
	type attempt struct {
		lock    *recordLock
		err     error
		elapsed time.Duration
	}
	done := make(chan attempt, 1)
	go func() {
		start := time.Now()
		lock, err := lockRecord(ctx, lockPath)
		done <- attempt{lock: lock, err: err, elapsed: time.Since(start)}
	}()

	select {
	case got := <-done:
		if got.lock != nil {
			_ = got.lock.release()
			t.Fatal("lockRecord acquired a lock another descriptor holds")
		}
		if !errors.Is(got.err, context.DeadlineExceeded) {
			t.Fatalf("lockRecord blocked by a held lock, context timing out: err = %v, "+
				"want it to wrap context.DeadlineExceeded", got.err)
		}
		if got.elapsed > 2*time.Second {
			t.Fatalf("lockRecord took %v to return after its context's deadline expired, "+
				"want it prompt", got.elapsed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("lockRecord never returned after its context's deadline expired: it is " +
			"blocking in LOCK_EX and ignoring the caller's context, so a stopped holder " +
			"would block every token write with no deadline")
	}
}

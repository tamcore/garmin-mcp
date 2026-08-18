//go:build unix

package cmd_test

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestRepairPermissionsNeverBlocksOnAContentOpen is the RED case for item 7:
// none of this package's other tests distinguish a metadata-only check
// (Lstat, then a descriptor open used only for chmod) from a real content
// read. A mutant that added an ordinary os.ReadFile(path) somewhere in this
// command's path — and discarded the bytes — would pass every other test
// in this file, because every seeded target is a small regular file a
// content read returns from immediately.
//
// A FIFO makes the difference observable: opening one for a real,
// data-carrying read blocks forever with no writer on the other end, while
// an Lstat, or a non-blocking open used only to fstat and chmod the
// descriptor, returns immediately regardless. Placing a FIFO at the exact
// name and directory a key file target's name pattern matches, then running
// the command with a bounded timeout, catches a mutant that reads content
// before, or regardless of, the type check that would otherwise refuse this
// FIFO (inspectTarget's wrongType, and — one layer further in, defense in
// depth — securefile's own regular-file check inside RestrictExisting). A
// content read added strictly after both of those gates, in the branch that
// only ever runs for a REGULAR fixable file, would return fast and not hang
// on this FIFO, and is not something this test — or any test that does not
// trace the process's own syscalls — can distinguish from a metadata-only
// fix; that residual gap is real and is not something this test claims to
// close.
//
// The run happens in a goroutine bounded by a timeout rather than an actual
// subprocess: it exercises the identical in-process command path every
// other test in this file does, and a timeout is checked instead of trusting
// a fixed sleep, so the test fails fast on a real regression rather than
// merely running slow.
func TestRepairPermissionsNeverBlocksOnAContentOpen(t *testing.T) {
	clearGarminEnv(t)
	stateDir := repairPermsStateDir(t)
	keysDir := filepath.Join(stateDir, "keys")
	if err := os.MkdirAll(keysDir, 0o700); err != nil {
		t.Fatalf("create keys dir: %v", err)
	}

	fifoPath := filepath.Join(keysDir, "key-v1.json")
	if err := syscall.Mkfifo(fifoPath, 0o644); err != nil {
		t.Fatalf("create FIFO %q: %v", fifoPath, err)
	}
	// Nothing ever opens the write end: a content-carrying read on fifoPath
	// blocks forever, which is exactly the property this test depends on.

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = runCommand(t, cmdRepairPermissions, "--state-dir="+stateDir)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("repair-permissions did not return within 5s against a FIFO named like a key file; " +
			"it appears to have opened the FIFO for a real, data-carrying read instead of treating it " +
			"as metadata only")
	}
}

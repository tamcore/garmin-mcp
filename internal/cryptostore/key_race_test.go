package cryptostore

import (
	"errors"
	"io/fs"
	"sync"
	"testing"
)

// TestInstallKeyFileRefusesToReplaceExistingMaterial states the no-replace rule
// directly: once material for a version is on disk it is the only material for that
// version, because every record already sealed under it becomes unreadable
// otherwise.
func TestInstallKeyFileRefusesToReplaceExistingMaterial(t *testing.T) {
	dir := tempDir(t)
	winner := mustKey(t, 1)
	if err := installKeyFile(dir, winner); err != nil {
		t.Fatalf("first installKeyFile: %v", err)
	}

	err := installKeyFile(dir, mustKey(t, 1))
	if !errors.Is(err, fs.ErrExist) {
		t.Fatalf("second installKeyFile: err = %v, want it to wrap fs.ErrExist", err)
	}
	loaded, err := LoadKey(dir, 1)
	if err != nil {
		t.Fatalf("LoadKey: %v", err)
	}
	if string(loaded.bytes()) != string(winner.bytes()) {
		t.Fatal("the second install replaced the material the first one committed")
	}
}

// TestLoadOrCreateKeyNeverSplitsMemoryFromDisk covers the concurrent-creation race.
// Every creator must end up holding exactly the material a later restart will load;
// a creator that keeps a key the disk does not have would seal records nobody can
// ever open again.
func TestLoadOrCreateKeyNeverSplitsMemoryFromDisk(t *testing.T) {
	const creators = 16
	dir := tempDir(t)

	keys := make([]Key, creators)
	failures := make([]error, creators)
	start := make(chan struct{})

	var group sync.WaitGroup
	for index := range creators {
		group.Go(func() {
			<-start
			keys[index], failures[index] = LoadOrCreateKey(dir, 1)
		})
	}
	close(start)
	group.Wait()

	onDisk, err := LoadKey(dir, 1)
	if err != nil {
		t.Fatalf("LoadKey after concurrent creation: %v", err)
	}
	for index := range creators {
		if failures[index] != nil {
			t.Fatalf("creator %d: %v", index, failures[index])
		}
		if string(keys[index].bytes()) != string(onDisk.bytes()) {
			t.Fatalf("creator %d holds material the disk does not have", index)
		}
	}
}

// TestLoadOrCreateKeyAcrossVersionsStaysIndependent keeps the loop above from hiding
// a version mix-up: two versions must each keep their own material.
func TestLoadOrCreateKeyAcrossVersionsStaysIndependent(t *testing.T) {
	dir := tempDir(t)

	first, err := LoadOrCreateKey(dir, 1)
	if err != nil {
		t.Fatalf("LoadOrCreateKey version 1: %v", err)
	}
	second, err := LoadOrCreateKey(dir, 2)
	if err != nil {
		t.Fatalf("LoadOrCreateKey version 2: %v", err)
	}
	if string(first.bytes()) == string(second.bytes()) {
		t.Fatal("two key versions share material")
	}

	reloaded, err := LoadKey(dir, 1)
	if err != nil {
		t.Fatalf("LoadKey version 1: %v", err)
	}
	if string(reloaded.bytes()) != string(first.bytes()) {
		t.Fatal("creating version 2 changed version 1")
	}
}

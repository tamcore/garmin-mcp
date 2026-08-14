package store

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
)

// rewriteWrapper edits one field of the on-disk record wrapper, leaving the sealed
// payload untouched. It is the filesystem attacker: it cannot forge a ciphertext,
// but it can rewrite anything the AEAD does not authenticate.
func rewriteWrapper(t *testing.T, store *FileStore, mutate func(*storedRecord)) {
	t.Helper()
	path := store.recordPath(testPrincipal)

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read record: %v", err)
	}
	var record storedRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatalf("record is not JSON: %v", err)
	}
	mutate(&record)

	edited, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("encode record: %v", err)
	}
	if err := os.WriteFile(path, edited, 0o600); err != nil {
		t.Fatalf("write record: %v", err)
	}
}

func saveOnce(t *testing.T, store *FileStore) {
	t.Helper()
	if _, err := store.Save(context.Background(), testPrincipal, newTestTokens(), 0); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

func TestLoadRefusesARewrittenRecordVersion(t *testing.T) {
	store, _ := newTestStore(t)
	saveOnce(t, store)

	rewriteWrapper(t, store, func(record *storedRecord) { record.Version = 42 })

	if _, _, err := store.Load(context.Background(), testPrincipal); !errors.Is(err, ErrCorruptRecord) {
		t.Fatalf("Load of a record with a rewritten version: err = %v, want ErrCorruptRecord", err)
	}
}

// TestSaveRefusesARewrittenRecordVersionInsteadOfOverflowing is the availability
// half: a version pushed to the maximum would make the next save compute a negative
// version, so the rewrite must be detected instead of acted on.
func TestSaveRefusesARewrittenRecordVersionInsteadOfOverflowing(t *testing.T) {
	store, _ := newTestStore(t)
	saveOnce(t, store)

	rewriteWrapper(t, store, func(record *storedRecord) { record.Version = math.MaxInt64 })

	version, err := store.Save(context.Background(), testPrincipal, newTestTokens(), math.MaxInt64)
	if !errors.Is(err, ErrCorruptRecord) {
		t.Fatalf("Save onto a record with a rewritten version: err = %v, want ErrCorruptRecord", err)
	}
	if version > 0 {
		t.Fatalf("Save reported version %d for a refused write", version)
	}
}

func TestLoadRefusesARewrittenRecordSchema(t *testing.T) {
	store, _ := newTestStore(t)
	saveOnce(t, store)

	rewriteWrapper(t, store, func(record *storedRecord) { record.Schema = recordSchema + 1 })

	if _, _, err := store.Load(context.Background(), testPrincipal); !errors.Is(err, ErrCorruptRecord) {
		t.Fatalf("Load of a record with a rewritten schema: err = %v, want ErrCorruptRecord", err)
	}
}

// TestRecordAADBindsSchemaAndVersion states the binding where it is implemented, so
// a later refactor that drops one of the two fields fails here rather than in a
// subtle on-disk way.
func TestRecordAADBindsSchemaAndVersion(t *testing.T) {
	base := recordAAD(recordSchema, 1)

	for name, other := range map[string]string{
		"a different version": recordAAD(recordSchema, 2),
		"a different schema":  recordAAD(recordSchema+1, 1),
	} {
		if other == base {
			t.Fatalf("the additional data does not distinguish %s", name)
		}
	}
	for _, part := range []string{recordType, strconv.Itoa(recordSchema)} {
		if !strings.Contains(base, part) {
			t.Fatalf("the additional data %q no longer names %q", base, part)
		}
	}
}

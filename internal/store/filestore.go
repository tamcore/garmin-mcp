package store

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/tamcore/garmin-mcp/internal/cryptostore"
)

// recordType is the record kind bound into the AEAD additional data. A token
// record therefore cannot be opened as any other kind of record.
const recordType = "garmin_di_tokens"

// recordSchema is the on-disk wrapper version. It changes only when the wrapper
// fields change; the payload format is versioned by cryptostore.
//
// Schema 2 binds the wrapper's schema and version into the AEAD additional data.
// A schema 1 record cannot be opened by this code, and reports ErrCorruptRecord
// rather than failing silently.
const recordSchema = 2

// maxRecordVersion bounds the compare-and-set counter far below the point where
// the next increment could overflow, so a rewritten wrapper cannot turn a save
// into a negative version. Reaching it honestly would take more saves than a
// deployment can perform.
const maxRecordVersion = int64(1) << 62

// recordsDirName is the subdirectory holding one file per principal.
const recordsDirName = "tokens"

// Config configures a FileStore. Every field is explicit: nothing here is read
// from the environment.
type Config struct {
	// Dir is the store root. It must be a real path with no symlinked component,
	// and it is created with mode 0700 if absent.
	Dir string

	// Key encrypts and decrypts records, and is the active key every write is
	// sealed under. Obtain it from cryptostore.LoadOrCreateKey, so that key
	// material stays owner-only.
	Key cryptostore.Key

	// RetiredKeys are additional key versions kept only to read a record a staged
	// rotation has not yet re-sealed onto Key. Never used to seal a write. A nil
	// slice is the pre-rotation shape: every record must already be sealed under
	// Key.
	RetiredKeys []cryptostore.Key

	// AllowInsecureInlineTokens enables the inline token JSON compatibility
	// override. It is unsafe and must stay false in remote mode; see inline.go.
	AllowInsecureInlineTokens bool
}

// FileStore is a local token store: one AEAD-encrypted file per principal.
//
// Concurrency: a per-principal mutex serializes the read-modify-write of one
// record, and the compare-and-set version makes a lost update detectable instead
// of silent. That covers concurrent goroutines in one process, which is the
// single-active-instance deployment this store is built for. Two processes sharing
// a directory could still both pass their version check before either renames;
// that case belongs to the shared-storage implementation, not here.
//
// A FileStore is safe for concurrent use and holds no package-level state.
type FileStore struct {
	root        string
	records     string
	crypt       keySet
	allowInline bool
	locks       *principalLocks
}

// NewFileStore validates cfg, creates the store directories with owner-only
// permissions and returns the store.
func NewFileStore(cfg Config) (*FileStore, error) {
	if strings.TrimSpace(cfg.Dir) == "" {
		return nil, fmt.Errorf("store: no directory configured: %w", ErrInvalidConfig)
	}
	crypt, err := newKeySet(cfg.Key, cfg.RetiredKeys)
	if err != nil {
		return nil, err
	}

	root, err := ResolveStoreDir(cfg.Dir)
	if err != nil {
		return nil, err
	}
	// Both levels are made owner-only: the root, because it may already exist with
	// a mode inherited from the umask, and the records directory, because the
	// listing of principals is itself information.
	if err := ensureOwnerOnlyDir(root); err != nil {
		return nil, err
	}
	records := filepath.Join(root, recordsDirName)
	if err := ensureOwnerOnlyDir(records); err != nil {
		return nil, err
	}

	return &FileStore{
		root:        root,
		records:     records,
		crypt:       crypt,
		allowInline: cfg.AllowInsecureInlineTokens,
		locks:       newPrincipalLocks(),
	}, nil
}

// ResolveStoreDir validates a store root: it refuses an empty path, a ~username
// path and any symlinked component, and expands a leading ~ for the current user.
func ResolveStoreDir(dir string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", fmt.Errorf("store: empty store directory: %w", ErrInvalidConfig)
	}
	expanded, err := expandHome(dir)
	if err != nil {
		return "", err
	}
	if err := checkPathAncestry(expanded); err != nil {
		return "", err
	}
	return expanded, nil
}

// checkKeyUsable probes the key with a throwaway seal. cryptostore exposes no
// accessor for its material — deliberately — so a probe is the only way to reject
// the zero Key before it fails on the first Save.
func checkKeyUsable(key cryptostore.Key) error {
	if _, err := cryptostore.Encrypt(key, "store-config-probe", recordType, nil); err != nil {
		return fmt.Errorf("store: unusable encryption key: %w: %w", ErrInvalidConfig, err)
	}
	return nil
}

// AllowsInlineTokens reports whether the insecure inline token override is on.
func (s *FileStore) AllowsInlineTokens() bool { return s.allowInline }

// Load returns the token set for principal and the record version that produced
// it. It reports ErrNoTokens when no record exists, which is the signal to log in.
func (s *FileStore) Load(ctx context.Context, principal string) (TokenSet, int64, error) {
	if err := checkRequest(ctx, principal); err != nil {
		return TokenSet{}, 0, err
	}

	unlock := s.locks.rlock(principal)
	defer unlock()

	record, err := s.readRecord(principal)
	if err != nil {
		return TokenSet{}, 0, err
	}
	set, err := s.openRecord(principal, record)
	if err != nil {
		return TokenSet{}, 0, err
	}
	return set, record.Version, nil
}

// Save stores set for principal and returns the new version.
//
// expectedVersion is a compare-and-set precondition: zero means the record must
// not exist yet, and any other value must equal the stored version. A mismatch
// reports ErrVersionConflict and changes nothing, so a caller that raced a token
// rotation reloads and retries instead of overwriting the newer refresh token.
func (s *FileStore) Save(ctx context.Context, principal string, set TokenSet, expectedVersion int64) (int64, error) {
	if err := checkRequest(ctx, principal); err != nil {
		return 0, err
	}

	unlock := s.locks.lock(principal)
	defer unlock()

	// The in-process mutex above covers concurrent goroutines inside this
	// *FileStore only. This cross-process lock is what makes Save's own
	// read-then-write critical section run exclusively of a concurrent
	// Reseal from a SEPARATE *FileStore in a rotate-key process — see
	// filestore_reseal.go and filelock_unix.go for why the in-process mutex
	// alone is not enough there.
	crossLock, err := lockRecord(s.lockPath(principal))
	if err != nil {
		return 0, fmt.Errorf("store: lock record for save: %w", err)
	}
	defer func() { _ = crossLock.release() }()

	current, err := s.currentVersion(principal)
	if err != nil {
		return 0, err
	}
	if current != expectedVersion {
		return 0, fmt.Errorf("store: record is at version %d, caller expected %d: %w",
			current, expectedVersion, ErrVersionConflict)
	}
	return s.commit(principal, set, current+1)
}

// recordAAD binds a record to the wrapper that carries it.
//
// The schema and the version cannot live inside the ciphertext: a reader needs both
// before it can decide how to read the file at all. They are authenticated as
// additional data instead, so rewriting either one makes the AEAD fail rather than
// silently changing the compare-and-set state. The record type stays the prefix, so
// a record still cannot be opened as a different kind of record.
func recordAAD(schema int, version int64) string {
	return recordType + "/schema=" + strconv.Itoa(schema) +
		"/version=" + strconv.FormatInt(version, 10)
}

// commit seals set and writes it atomically as the given version.
func (s *FileStore) commit(principal string, set TokenSet, version int64) (int64, error) {
	payload, err := s.crypt.encrypt(principal,
		recordAAD(recordSchema, version), encodeRecordPayload(set))
	if err != nil {
		return 0, fmt.Errorf("store: seal record: %w", err)
	}
	content, err := json.Marshal(storedRecord{
		Schema:  recordSchema,
		Version: version,
		Payload: base64.StdEncoding.EncodeToString(payload),
	})
	if err != nil {
		return 0, fmt.Errorf("store: encode record: %w", err)
	}

	if err := ensureOwnerOnlyDir(s.records); err != nil {
		return 0, err
	}
	if err := writeFileAtomically(s.recordPath(principal), content); err != nil {
		return 0, err
	}
	return version, nil
}

// Delete removes the local record for principal. An absent record is not an error.
//
// This does NOT revoke anything at Garmin. The DI refresh token stays valid at
// Garmin's service until Garmin expires or revokes it, and any copy of the file
// keeps working. Never report a Delete as a revocation.
func (s *FileStore) Delete(ctx context.Context, principal string) error {
	if err := checkRequest(ctx, principal); err != nil {
		return err
	}

	unlock := s.locks.lock(principal)
	defer unlock()

	// Held for the same reason Save holds it: the in-process mutex above covers
	// goroutines inside this *FileStore only. Without the cross-process lock, a
	// Save in another process can read the current version, have this Delete
	// remove the record underneath it, and then write — resurrecting a record an
	// operator deliberately unlinked. Deleting under the lock orders the two.
	crossLock, err := lockRecord(s.lockPath(principal))
	if err != nil {
		return fmt.Errorf("store: lock record for delete: %w", err)
	}
	defer func() { _ = crossLock.release() }()

	return removeFile(s.recordPath(principal))
}

// recordPath is the file holding one principal's record. The name is a SHA-256
// digest of the principal id, so the file name is a fixed-length safe string and
// the identifier is not written to the filesystem in the clear.
func (s *FileStore) recordPath(principal string) string {
	digest := sha256.Sum256([]byte(principal))
	return filepath.Join(s.records, hex.EncodeToString(digest[:])+".json")
}

// lockPath is the OS-level advisory lock file (filelock_unix.go) that
// serializes one principal's record across separate processes. It is a
// sibling of the record file, never the record file itself.
func (s *FileStore) lockPath(principal string) string {
	return s.recordPath(principal) + ".lock"
}

// storedRecord is the on-disk wrapper. It carries no principal and no key version:
// the file name identifies the principal, and the key version lives inside the
// envelope, where it is authenticated.
type storedRecord struct {
	Schema  int    `json:"schema"`
	Version int64  `json:"version"`
	Payload string `json:"payload"`
}

// readRecord reads and parses the wrapper without decrypting the payload.
func (s *FileStore) readRecord(principal string) (storedRecord, error) {
	raw, err := readOwnerOnlyFile(s.recordPath(principal), ErrNoTokens)
	if err != nil {
		return storedRecord{}, err
	}

	var record storedRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return storedRecord{}, fmt.Errorf("store: not a record document (%d bytes): %w",
			len(raw), ErrCorruptRecord)
	}
	if record.Schema != recordSchema || record.Version <= 0 ||
		record.Version > maxRecordVersion || record.Payload == "" {
		return storedRecord{}, fmt.Errorf("store: record has schema %d and version %d: %w",
			record.Schema, record.Version, ErrCorruptRecord)
	}
	return record, nil
}

// openRecord decrypts and decodes one record.
func (s *FileStore) openRecord(principal string, record storedRecord) (TokenSet, error) {
	sealed, err := base64.StdEncoding.DecodeString(record.Payload)
	if err != nil {
		return TokenSet{}, fmt.Errorf("store: record payload is not base64: %w", ErrCorruptRecord)
	}
	plaintext, _, err := s.crypt.decrypt(principal,
		recordAAD(record.Schema, record.Version), sealed)
	if err != nil {
		// The cause is wrapped for operator diagnosis: it reports versions and
		// sizes only, never material.
		return TokenSet{}, fmt.Errorf("store: open record: %w: %w", ErrCorruptRecord, err)
	}
	return decodeTokenDocument(plaintext, sourceRecord)
}

// currentVersion reports the stored version, or 0 when no record exists.
//
// The wrapper's version is only trusted once the AEAD has authenticated it, so the
// record is opened here even though the caller may not need its content. A rewritten
// version therefore reports ErrCorruptRecord instead of moving the compare-and-set
// state.
func (s *FileStore) currentVersion(principal string) (int64, error) {
	record, err := s.readRecord(principal)
	switch {
	case errors.Is(err, ErrNoTokens):
		return 0, nil
	case err != nil:
		return 0, err
	}
	if _, err := s.openRecord(principal, record); err != nil {
		return 0, err
	}
	return record.Version, nil
}

// checkRequest validates the two things every operation needs.
func checkRequest(ctx context.Context, principal string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("store: request cancelled: %w", err)
	}
	if strings.TrimSpace(principal) == "" {
		return fmt.Errorf("store: empty principal: %w", ErrInvalidPrincipal)
	}
	return nil
}

// principalLocks hands out one mutex per principal, so two principals never block
// each other. It is store state, not package state.
type principalLocks struct {
	locks sync.Map // principal -> *sync.RWMutex
}

func newPrincipalLocks() *principalLocks { return &principalLocks{} }

func (p *principalLocks) mutex(principal string) *sync.RWMutex {
	existing, _ := p.locks.LoadOrStore(principal, &sync.RWMutex{})
	mutex, _ := existing.(*sync.RWMutex)
	return mutex
}

// lock takes the write lock and returns its release function.
func (p *principalLocks) lock(principal string) func() {
	mutex := p.mutex(principal)
	mutex.Lock()
	return mutex.Unlock
}

// rlock takes the read lock and returns its release function.
func (p *principalLocks) rlock(principal string) func() {
	mutex := p.mutex(principal)
	mutex.RLock()
	return mutex.RUnlock
}

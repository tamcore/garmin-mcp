package store_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/store"
)

// The transaction contract the authorization server depends on: a compare-and-set that
// refuses a stale write, and a read-and-delete that elects exactly one winner. The race
// tests run real goroutines so -race has something to observe.

// testClientState is a synthetic opaque client state. It is deliberately a value the
// store must return byte for byte, spaces and punctuation included.
const testClientState = `opaque client state {"nonce":"abc"} +/=`

// seedRichTransaction stores a transaction that carries a resource and a client state.
func seedRichTransaction(t *testing.T, s *store.SQLiteStore, clientID string) store.Secret {
	t.Helper()
	handle := store.NewSecret("rich-" + testHandle)
	err := s.PutAuthTransaction(context.Background(), store.AuthTransactionDraft{
		Handle:        handle,
		ClientID:      clientID,
		RedirectURI:   testRedirectURI,
		Scopes:        []string{testScope},
		Resource:      testAudience,
		ClientState:   store.NewSecret(testClientState),
		CodeChallenge: testChallenge,
		Lifetime:      10 * time.Minute,
	})
	if err != nil {
		t.Fatalf("PutAuthTransaction: %v", err)
	}
	return handle
}

// TestTransactionCarriesResourceAndClientState is the round trip of the two fields 0001
// had no column for. The state must come back exactly as it went in, because a client
// compares it byte for byte.
func TestTransactionCarriesResourceAndClientState(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	client := seedClient(t, opened)
	handle := seedRichTransaction(t, opened, client.ID)

	transaction, err := opened.AuthTransaction(ctx, handle)
	if err != nil {
		t.Fatalf("AuthTransaction: %v", err)
	}
	if transaction.Resource != testAudience {
		t.Errorf("Resource = %q, want %q", transaction.Resource, testAudience)
	}
	if transaction.ClientState.Reveal() != testClientState {
		t.Errorf("ClientState = %q, want %q", transaction.ClientState.Reveal(), testClientState)
	}
	if transaction.Version != 0 {
		t.Errorf("Version = %d, want 0 for a freshly created transaction", transaction.Version)
	}
}

// TestClientStateIsNotOnDiskInTheClear: the state belongs to someone else, so it is
// sealed like every other third-party value in this schema.
func TestClientStateIsNotOnDiskInTheClear(t *testing.T) {
	t.Parallel()
	opened, _, path := newTestStoreWithPath(t)
	client := seedClient(t, opened)
	seedRichTransaction(t, opened, client.ID)

	for _, suffix := range []string{"", "-wal"} {
		raw, err := os.ReadFile(path + suffix)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatalf("read %s: %v", path+suffix, err)
		}
		if bytes.Contains(raw, []byte(testClientState)) {
			t.Fatalf("the client state is in the clear in %s", path+suffix)
		}
	}
}

// TestUpdateAuthTransactionIsACompareAndSet: the write lands once, the version advances,
// and the same expected version cannot be used twice.
func TestUpdateAuthTransactionIsACompareAndSet(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	principal := seedPrincipal(t, opened)
	client := seedClient(t, opened)
	handle := seedRichTransaction(t, opened, client.ID)

	update := store.AuthTransactionUpdate{
		Handle:      handle,
		PrincipalID: principal.ID,
		Scopes:      []string{testScope},
		Resource:    testAudience,
		ClientState: store.NewSecret(testClientState),
	}
	next, err := opened.UpdateAuthTransaction(ctx, update, 0)
	if err != nil {
		t.Fatalf("UpdateAuthTransaction: %v", err)
	}
	if next != 1 {
		t.Fatalf("new version = %d, want 1", next)
	}

	transaction, err := opened.AuthTransaction(ctx, handle)
	if err != nil {
		t.Fatalf("AuthTransaction: %v", err)
	}
	if transaction.PrincipalID != principal.ID || transaction.Version != 1 {
		t.Fatalf("stored transaction = principal %q version %d, want %q and 1",
			transaction.PrincipalID, transaction.Version, principal.ID)
	}

	_, err = opened.UpdateAuthTransaction(ctx, update, 0)
	if !errors.Is(err, store.ErrTransactionConflict) {
		t.Fatalf("a stale update: err = %v, want ErrTransactionConflict", err)
	}
}

// TestUpdateAuthTransactionSeparatesGoneFromAdvanced is the distinction one UPDATE
// cannot make on its own: both cases affect zero rows.
func TestUpdateAuthTransactionSeparatesGoneFromAdvanced(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	client := seedClient(t, opened)
	handle := seedRichTransaction(t, opened, client.ID)

	update := store.AuthTransactionUpdate{Handle: handle, Scopes: []string{testScope}}
	if _, err := opened.UpdateAuthTransaction(ctx, update, 0); err != nil {
		t.Fatalf("UpdateAuthTransaction: %v", err)
	}
	if _, err := opened.UpdateAuthTransaction(ctx, update, 7); !errors.Is(
		err, store.ErrTransactionConflict) {
		t.Fatalf("a wrong version on a live row: err = %v, want ErrTransactionConflict", err)
	}

	if err := opened.DeleteAuthTransaction(ctx, handle); err != nil {
		t.Fatalf("DeleteAuthTransaction: %v", err)
	}
	if _, err := opened.UpdateAuthTransaction(ctx, update, 1); !errors.Is(
		err, store.ErrTransactionNotFound) {
		t.Fatalf("an update of a deleted row: err = %v, want ErrTransactionNotFound", err)
	}
}

// TestUpdateAuthTransactionAcceptsAnEmptyScopeSetAndNoResource: a request that asks for
// nothing and names no resource has to be storable, or a caller must invent a value.
func TestUpdateAuthTransactionAcceptsAnEmptyScopeSetAndNoResource(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	client := seedClient(t, opened)
	handle := seedRichTransaction(t, opened, client.ID)

	if _, err := opened.UpdateAuthTransaction(ctx,
		store.AuthTransactionUpdate{Handle: handle}, 0); err != nil {
		t.Fatalf("UpdateAuthTransaction with no scopes and no resource: %v", err)
	}

	transaction, err := opened.AuthTransaction(ctx, handle)
	if err != nil {
		t.Fatalf("AuthTransaction: %v", err)
	}
	if len(transaction.Scopes) != 0 || transaction.Resource != "" {
		t.Fatalf("stored scopes %v and resource %q, want none and empty",
			transaction.Scopes, transaction.Resource)
	}
	if !transaction.ClientState.IsZero() {
		t.Fatal("clearing the state left one behind")
	}
}

// TestConsumeAuthTransactionReturnsAndDeletes: one call gets the record, and the row is
// gone afterwards.
func TestConsumeAuthTransactionReturnsAndDeletes(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	client := seedClient(t, opened)
	handle := seedRichTransaction(t, opened, client.ID)

	consumed, err := opened.ConsumeAuthTransaction(ctx, handle)
	if err != nil {
		t.Fatalf("ConsumeAuthTransaction: %v", err)
	}
	if consumed.ClientID != client.ID || consumed.ClientState.Reveal() != testClientState {
		t.Fatalf("consumed record = %+v, want the seeded one", consumed)
	}
	if _, err := opened.ConsumeAuthTransaction(ctx, handle); !errors.Is(
		err, store.ErrTransactionNotFound) {
		t.Fatalf("a second consumption: err = %v, want ErrTransactionNotFound", err)
	}
	if _, err := opened.AuthTransaction(ctx, handle); !errors.Is(
		err, store.ErrTransactionNotFound) {
		t.Fatalf("the transaction survived consumption: err = %v", err)
	}
}

// TestConsumeAuthTransactionReturnsAnExpiredRecord: discarding an expired transaction is
// what the call is for, so the row must go and the record must come back with its window
// for the caller to judge.
func TestConsumeAuthTransactionReturnsAnExpiredRecord(t *testing.T) {
	t.Parallel()
	opened, clock := newTestStore(t)
	ctx := context.Background()
	client := seedClient(t, opened)
	handle := seedRichTransaction(t, opened, client.ID)

	clock.advance(11 * time.Minute)
	if _, err := opened.AuthTransaction(ctx, handle); !errors.Is(
		err, store.ErrTransactionNotFound) {
		t.Fatalf("an expired transaction is readable: err = %v", err)
	}

	consumed, err := opened.ConsumeAuthTransaction(ctx, handle)
	if err != nil {
		t.Fatalf("ConsumeAuthTransaction on an expired transaction: %v", err)
	}
	if !consumed.IsExpired(clock.Now()) {
		t.Fatal("the consumed record does not report itself expired")
	}
	if _, err := opened.ConsumeAuthTransaction(ctx, handle); !errors.Is(
		err, store.ErrTransactionNotFound) {
		t.Fatalf("the expired transaction was not discarded: err = %v", err)
	}
}

// TestConcurrentConsumeAuthTransactionElectsOneWinner is the claim that makes completing
// an authorization single-use. A compare-and-set would let two callers each win their
// own, one after the other; the read-and-delete must not.
func TestConcurrentConsumeAuthTransactionElectsOneWinner(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	client := seedClient(t, opened)
	handle := seedRichTransaction(t, opened, client.ID)

	const callers = 8
	results := make([]error, callers)
	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	for index := range callers {
		done.Go(func() {
			start.Wait()
			_, results[index] = opened.ConsumeAuthTransaction(ctx, handle)
		})
	}
	start.Done()
	done.Wait()

	assertOneWinner(t, results, store.ErrTransactionNotFound)
}

// TestConcurrentUpdateAuthTransactionElectsOneWinner: every writer reads version 0 and
// tries to advance it. Exactly one may commit and the rest must be told to reload.
func TestConcurrentUpdateAuthTransactionElectsOneWinner(t *testing.T) {
	t.Parallel()
	opened, _ := newTestStore(t)
	ctx := context.Background()
	client := seedClient(t, opened)
	handle := seedRichTransaction(t, opened, client.ID)

	const writers = 4
	results := make([]error, writers)
	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	for index := range writers {
		resource := "https://resource.example/" + strconv.Itoa(index)
		done.Go(func() {
			start.Wait()
			_, results[index] = opened.UpdateAuthTransaction(ctx, store.AuthTransactionUpdate{
				Handle:   handle,
				Scopes:   []string{testScope},
				Resource: resource,
			}, 0)
		})
	}
	start.Done()
	done.Wait()

	assertOneWinner(t, results, store.ErrTransactionConflict)
}

// assertOneWinner requires exactly one nil result and loser errors that all match want.
func assertOneWinner(t *testing.T, results []error, want error) {
	t.Helper()
	var winners, losers int
	for index, err := range results {
		switch {
		case err == nil:
			winners++
		case errors.Is(err, want):
			losers++
		default:
			t.Fatalf("caller %d: unexpected error: %v", index, err)
		}
	}
	if winners != 1 || losers != len(results)-1 {
		t.Fatalf("got %d winners and %d losers across %d callers, want exactly 1 winner",
			winners, losers, len(results))
	}
}

package store

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// outcome is one racing writer's result.
type outcome struct {
	token   string
	version int64
	err     error
}

// electWinner asserts that exactly one writer committed and the other was told to
// retry, and returns the winner.
func electWinner(t *testing.T, results []outcome) outcome {
	t.Helper()
	var winners, conflicts int
	winner := outcome{}
	for _, result := range results {
		switch {
		case result.err == nil:
			winners++
			winner = result
		case errors.Is(result.err, ErrVersionConflict):
			conflicts++
		default:
			t.Fatalf("unexpected Save error: %v", result.err)
		}
	}
	if winners != 1 || conflicts != 1 {
		t.Fatalf("got %d winners and %d conflicts, want exactly 1 of each", winners, conflicts)
	}
	return winner
}

// TestConcurrentRotatedWritesElectOneWinner is the requirement that a rotated
// refresh token cannot be lost. Two writers read version 1 and both try to
// replace it: exactly one commits, and the other is told to retry through
// ErrVersionConflict rather than silently overwriting.
//
// Run under -race this also covers the per-principal serialization: the
// read-modify-write around one record must not interleave.
func TestConcurrentRotatedWritesElectOneWinner(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	base, err := store.Save(ctx, testPrincipal, newTestTokens(), 0)
	if err != nil {
		t.Fatalf("seed Save: %v", err)
	}

	results := make([]outcome, 2)
	tokens := []string{"rotated-by-writer-a", "rotated-by-writer-b"}

	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	for index, token := range tokens {
		done.Go(func() {
			start.Wait()
			version, err := store.Save(ctx, testPrincipal, newTestTokens().WithToken(token), base)
			results[index] = outcome{token: token, version: version, err: err}
		})
	}
	start.Done()
	done.Wait()

	winner := electWinner(t, results)
	if winner.version != base+1 {
		t.Fatalf("winning version = %d, want %d", winner.version, base+1)
	}

	loaded, version, err := store.Load(ctx, testPrincipal)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if version != winner.version || loaded.Token() != winner.token {
		t.Fatalf("stored record is version %d token %q, want version %d token %q",
			version, loaded.Token(), winner.version, winner.token)
	}
}

// TestConcurrentReadersAndWriterNeverSeeATornRecord exercises the atomic write: a
// reader sees either the previous record or the new one, never a partial file.
func TestConcurrentReadersAndWriterNeverSeeATornRecord(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	version, err := store.Save(ctx, testPrincipal, newTestTokens(), 0)
	if err != nil {
		t.Fatalf("seed Save: %v", err)
	}

	const writes = 20
	var group sync.WaitGroup
	group.Go(func() {
		current := version
		for i := range writes {
			next, err := store.Save(ctx, testPrincipal, newTestTokens().WithToken("rotated"), current)
			if err != nil {
				t.Errorf("Save %d: %v", i, err)
				return
			}
			current = next
		}
	})

	for range 4 {
		group.Go(func() {
			for range writes {
				set, _, err := store.Load(ctx, testPrincipal)
				if err != nil {
					t.Errorf("Load: %v", err)
					return
				}
				if set.RefreshToken() != testRefreshToken {
					t.Errorf("Load saw a torn record: refresh token %q", set.RefreshToken())
					return
				}
			}
		})
	}
	group.Wait()
}

// TestSeparatePrincipalsDoNotBlockEachOther documents that serialization is per
// principal, not global: two principals commit independently.
func TestSeparatePrincipalsDoNotBlockEachOther(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	principals := []string{testPrincipal, testOther}

	var group sync.WaitGroup
	for _, principal := range principals {
		group.Go(func() {
			if _, err := store.Save(ctx, principal, newTestTokens(), 0); err != nil {
				t.Errorf("Save for %s: %v", principal, err)
			}
		})
	}
	group.Wait()

	for _, principal := range principals {
		if _, version, err := store.Load(ctx, principal); err != nil || version != 1 {
			t.Fatalf("Load for %s: version %d err %v", principal, version, err)
		}
	}
}

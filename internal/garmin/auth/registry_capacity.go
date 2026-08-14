package auth

import "time"

// Capacity management for the transaction registry: making room for a new login,
// choosing what to give up under pressure, and dropping what aged out. Every
// function here runs under the registry's mutex.

// reserveLocked makes room for one new entry.
//
// Fairness rule: an entry nobody has submitted a code for is evicted before one
// that is in use, and among equals the oldest goes first. An entry whose completion
// is in flight is never evicted, because its external effects are already running.
func (r *Registry) reserveLocked() error {
	if len(r.entries) < r.cfg.MaxEntries {
		return nil
	}

	for len(r.entries) >= r.cfg.MaxEntries {
		key, entry := r.evictionCandidateLocked()
		if entry == nil {
			return ErrRegistryFull
		}
		r.failLocked(key, entry)
	}
	return nil
}

// evictionCandidateLocked picks the least valuable evictable entry, or nil when
// every resident entry is being completed.
func (r *Registry) evictionCandidateLocked() (string, *transaction) {
	var (
		bestKey   string
		best      *transaction
		bestScore int
	)
	for key, entry := range r.entries {
		if entry.completing {
			continue
		}
		score := 1
		if entry.attempted {
			score = 2
		}
		if best == nil || score < bestScore ||
			(score == bestScore && entry.createdAt.Before(best.createdAt)) {
			bestKey, best, bestScore = key, entry, score
		}
	}
	return bestKey, best
}

// expireLocked records the expiry on the entry's machine and drops it.
func (r *Registry) expireLocked(key string, entry *transaction) {
	if next, err := entry.pending.machineValue().Expire(); err == nil {
		entry.pending = entry.pending.withMachine(next)
	}
	delete(r.entries, key)
}

// failLocked records the failure on the entry's machine and drops it.
func (r *Registry) failLocked(key string, entry *transaction) {
	if next, err := entry.pending.machineValue().Fail(); err == nil {
		entry.pending = entry.pending.withMachine(next)
	}
	delete(r.entries, key)
}

// sweepLocked drops every entry whose absolute TTL elapsed, so an abandoned
// login cannot occupy a slot until the process restarts.
func (r *Registry) sweepLocked(now time.Time) {
	for key, entry := range r.entries {
		if !now.Before(entry.expiresAt) {
			r.expireLocked(key, entry)
		}
	}
}

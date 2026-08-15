package ratelimit

import "container/list"

// An lruTable is a bounded map that evicts its least recently used entry.
//
// Both limiters in this package need the same property and for the same reason:
// the key is chosen by a remote caller, so an unbounded table is a memory leak
// any caller can drive by presenting a stream of distinct keys. Keeping one
// implementation means the bound is enforced in one place rather than two.
//
// It is not safe for concurrent use. Every caller here holds its own mutex,
// which is also what keeps the budget refill and the table update atomic.
type lruTable[K comparable, V any] struct {
	max     int
	entries map[K]*tableEntry[K, V]
	// order lists keys by last use, least recent at the front.
	order *list.List
}

// A tableEntry is one value plus its position in the recency list.
type tableEntry[K comparable, V any] struct {
	value   V
	element *list.Element
}

// newLRUTable returns a table holding at most max entries. max is validated by
// the caller, which reports the misconfiguration in its own terms.
func newLRUTable[K comparable, V any](max int) *lruTable[K, V] {
	return &lruTable[K, V]{
		max:     max,
		entries: make(map[K]*tableEntry[K, V]),
		order:   list.New(),
	}
}

// touch returns the value for key, calling create when there is none, and marks
// the key most recently used. Creating a value may evict the oldest entry.
//
// Eviction resets the evicted key's budget, which is a deliberate accepted trade:
// refusing a new key because the table is full would let one caller lock every
// other caller out entirely.
func (t *lruTable[K, V]) touch(key K, create func() V) V {
	if existing, ok := t.entries[key]; ok {
		t.order.MoveToBack(existing.element)
		return existing.value
	}

	for len(t.entries) >= t.max {
		t.evictOldest()
	}

	created := &tableEntry[K, V]{value: create()}
	created.element = t.order.PushBack(key)
	t.entries[key] = created
	return created.value
}

// evictOldest drops the least recently used entry, if there is one.
func (t *lruTable[K, V]) evictOldest() {
	oldest := t.order.Front()
	if oldest == nil {
		return
	}
	t.order.Remove(oldest)
	key, ok := oldest.Value.(K)
	if !ok {
		return
	}
	delete(t.entries, key)
}

// len reports how many keys currently hold a value.
func (t *lruTable[K, V]) len() int { return len(t.entries) }

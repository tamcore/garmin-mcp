package auth_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/auth"
	"github.com/tamcore/garmin-mcp/internal/testkit"
)

// TestRegistryInterleavedTransactionsAreIsolated is the regression test for the
// upstream bug where a second MFA login overwrote the first login's pending
// state. Run under -race.
func TestRegistryInterleavedTransactionsAreIsolated(t *testing.T) {
	clock := testkit.NewFakeClock(registryStart())
	registry := newTestRegistry(t, clock, auth.RegistryConfig{MaxEntries: 64})

	const logins = 16
	var wg sync.WaitGroup
	wg.Add(logins)

	for i := range logins {
		go func(i int) {
			defer wg.Done()

			principal := fmt.Sprintf("principal-%d", i)
			csrf := fmt.Sprintf("csrf-secret-04%02d", i)
			cookie := fmt.Sprintf("cookie-secret-05%02d", i)

			id, err := registry.Create(pendingFor(principal, csrf, cookie))
			if err != nil {
				t.Errorf("%s: Create: %v", principal, err)
				return
			}

			pending, err := registry.Attempt(id, principal)
			if err != nil {
				t.Errorf("%s: Attempt: %v", principal, err)
				return
			}
			if pending.CSRFToken() != csrf {
				t.Errorf("%s: saw CSRF %q, want %q", principal, pending.CSRFToken(), csrf)
			}
			if got := pending.Cookies(); len(got) != 1 || got[0].Value != cookie {
				t.Errorf("%s: saw cookies %v, want %q", principal, got, cookie)
			}
			if err := registry.Complete(id); err != nil {
				t.Errorf("%s: Complete: %v", principal, err)
			}
		}(i)
	}

	wg.Wait()
	if registry.Len() != 0 {
		t.Fatalf("Len() = %d, want 0", registry.Len())
	}
}

// TestRegistryConcurrentCompleteIsSingleUse proves the terminal transition is
// won by exactly one caller. Run under -race.
func TestRegistryConcurrentCompleteIsSingleUse(t *testing.T) {
	clock := testkit.NewFakeClock(registryStart())
	registry := newTestRegistry(t, clock, auth.RegistryConfig{MaxAttempts: 32})

	id, err := registry.Create(pendingFor("principal-a", leakCSRF, leakCookie))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := registry.Attempt(id, "principal-a"); err != nil {
		t.Fatalf("Attempt: %v", err)
	}

	const callers = 8
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		succeeded int
	)
	wg.Add(callers)

	for range callers {
		go func() {
			defer wg.Done()
			if err := registry.Complete(id); err == nil {
				mu.Lock()
				succeeded++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if succeeded != 1 {
		t.Fatalf("%d callers completed the transaction, want exactly 1", succeeded)
	}
}

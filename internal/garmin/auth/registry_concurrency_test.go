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

			attempt, err := registry.Attempt(id, principal)
			if err != nil {
				t.Errorf("%s: Attempt: %v", principal, err)
				return
			}
			pending := attempt.Pending()
			if pending.CSRFToken() != csrf {
				t.Errorf("%s: saw CSRF %q, want %q", principal, pending.CSRFToken(), csrf)
			}
			if got := pending.Cookies(); len(got) != 1 || got[0].Value != cookie {
				t.Errorf("%s: saw cookies %v, want %q", principal, got, cookie)
			}
			if err := attempt.Claim(); err != nil {
				t.Errorf("%s: Claim: %v", principal, err)
			}
		}(i)
	}

	wg.Wait()
	if registry.Len() != 0 {
		t.Fatalf("Len() = %d, want 0", registry.Len())
	}
}

// TestRegistryConcurrentAttemptAdmitsOneCompletion proves that a burst of callers
// presenting one capability yields exactly one completion: the lease admits one, and
// the claim is single-use. Run under -race.
func TestRegistryConcurrentAttemptAdmitsOneCompletion(t *testing.T) {
	clock := testkit.NewFakeClock(registryStart())
	registry := newTestRegistry(t, clock, auth.RegistryConfig{MaxAttempts: 32})

	id, err := registry.Create(pendingFor(principalA, leakCSRF, leakCookie))
	if err != nil {
		t.Fatalf("Create: %v", err)
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

			attempt, err := registry.Attempt(id, principalA)
			if err != nil {
				return
			}
			defer attempt.Release()

			if err := attempt.Claim(); err == nil {
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
	if registry.Len() != 0 {
		t.Fatalf("Len() = %d, want 0", registry.Len())
	}
}

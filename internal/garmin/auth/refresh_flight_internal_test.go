package auth

import (
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	flightPrincipal = "principal-flight"
	flightClientID  = "GARMIN_CONNECT_MOBILE_ANDROID_DI"
	flightStored    = "di-token-flight-stored-0700"
	flightRefresh   = "di-refresh-flight-stored-0701"
	flightRotated   = "di-token-flight-rotated-0702"
)

// TestFinishFlightPublishesAndRetiresAtomically forces the interleaving the
// review named: a caller arriving while a finished refresh is being retired must
// not become a second leader and start a redundant rotation.
//
// The onFlightRetire seam runs at the moment before the result is published. The
// retirement is atomic only if it holds the mutex there, so the late caller is
// blocked in joinFlight and provably cannot reach the transport while the seam
// runs. A plain -race run cannot show this, because the window is a few
// instructions wide.
func TestFinishFlightPublishesAndRetiresAtomically(t *testing.T) {
	var (
		calls    atomic.Int64
		lateHit  = make(chan struct{})
		startAt  = make(chan struct{})
		lateOnce sync.Once
		hookOnce sync.Once
	)

	doer := funcDoer{fn: func(*http.Request) (*http.Response, error) {
		if calls.Add(1) > 1 {
			lateOnce.Do(func() { close(lateHit) })
		}
		return jsonBody(
			`{"access_token":"` + flightRotated + `","refresh_token":"` + flightRefresh + `"}`), nil
	}}

	store := newMemStore()
	store.put(NewTokenSet(flightStored, flightRefresh, flightClientID, internalStart()), 1)

	refresher, err := NewRefresher(RefreshConfig{
		Hosts:     internalHosts(t),
		Transport: doer,
		Store:     store,
		Clock:     fixedClock{at: internalStart()},
		onFlightRetire: func() {
			hookOnce.Do(func() {
				close(startAt)
				select {
				case <-lateHit:
					t.Error("a caller became a second leader while the flight was being retired")
				case <-time.After(200 * time.Millisecond):
				}
			})
		},
	})
	if err != nil {
		t.Fatalf("NewRefresher: %v", err)
	}

	var wg sync.WaitGroup
	wg.Go(func() {

		<-startAt
		if _, err := refresher.Refresh(t.Context(), flightPrincipal); err != nil {
			t.Errorf("late Refresh: %v", err)
		}
	})

	if _, err := refresher.Refresh(t.Context(), flightPrincipal); err != nil {
		t.Fatalf("leading Refresh: %v", err)
	}
	wg.Wait()
}

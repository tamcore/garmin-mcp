package auth_test

import (
	"errors"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/auth"
	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
)

// allStates is every state the machine models. A new state must be added here,
// which forces the transition matrix below to cover it.
func allStates() []auth.State {
	return []auth.State{
		auth.StateCreated,
		auth.StateCredentialsSubmitted,
		auth.StateMFAPending,
		auth.StateAuthenticated,
		auth.StateFailed,
		auth.StateExpired,
		auth.StateCancelled,
	}
}

// apply runs one named transition on m, so the matrix test can drive every
// (state, transition) pair through a single call site.
func apply(m auth.Machine, tr auth.Transition) (auth.Machine, error) {
	switch tr {
	case auth.TransitionSubmitCredentials:
		return m.SubmitCredentials(auth.StrategyMobileIOS)
	case auth.TransitionRequireMFA:
		return m.RequireMFA(protocol.MFAMethodEmail)
	case auth.TransitionAuthenticate:
		return m.Authenticate()
	case auth.TransitionVerifyMFA:
		return m.VerifyMFA()
	case auth.TransitionFail:
		return m.Fail()
	case auth.TransitionExpire:
		return m.Expire()
	case auth.TransitionCancel:
		return m.Cancel()
	default:
		panic("unknown transition in test: " + string(tr))
	}
}

// reach returns a machine parked in state, built only from permitted transitions.
func reach(t *testing.T, state auth.State) auth.Machine {
	t.Helper()

	m := auth.NewMachine()
	steps := map[auth.State][]auth.Transition{
		auth.StateCreated:              nil,
		auth.StateCredentialsSubmitted: {auth.TransitionSubmitCredentials},
		auth.StateMFAPending:           {auth.TransitionSubmitCredentials, auth.TransitionRequireMFA},
		auth.StateAuthenticated:        {auth.TransitionSubmitCredentials, auth.TransitionAuthenticate},
		auth.StateFailed:               {auth.TransitionFail},
		auth.StateExpired:              {auth.TransitionExpire},
		auth.StateCancelled:            {auth.TransitionCancel},
	}

	for _, tr := range steps[state] {
		next, err := apply(m, tr)
		if err != nil {
			t.Fatalf("building state %s: transition %s: %v", state, tr, err)
		}
		m = next
	}
	if m.State() != state {
		t.Fatalf("building state %s: reached %s", state, m.State())
	}
	return m
}

// permitted is the authoritative transition table this package promises.
func permitted() map[auth.State]map[auth.Transition]auth.State {
	return map[auth.State]map[auth.Transition]auth.State{
		auth.StateCreated: {
			auth.TransitionSubmitCredentials: auth.StateCredentialsSubmitted,
			auth.TransitionFail:              auth.StateFailed,
			auth.TransitionExpire:            auth.StateExpired,
			auth.TransitionCancel:            auth.StateCancelled,
		},
		auth.StateCredentialsSubmitted: {
			auth.TransitionRequireMFA:   auth.StateMFAPending,
			auth.TransitionAuthenticate: auth.StateAuthenticated,
			auth.TransitionFail:         auth.StateFailed,
			auth.TransitionExpire:       auth.StateExpired,
			auth.TransitionCancel:       auth.StateCancelled,
		},
		auth.StateMFAPending: {
			auth.TransitionVerifyMFA: auth.StateAuthenticated,
			auth.TransitionFail:      auth.StateFailed,
			auth.TransitionExpire:    auth.StateExpired,
			auth.TransitionCancel:    auth.StateCancelled,
		},
		auth.StateAuthenticated: {},
		auth.StateFailed:        {},
		auth.StateExpired:       {},
		auth.StateCancelled:     {},
	}
}

func TestMachineTransitionMatrix(t *testing.T) {
	table := permitted()

	for _, from := range allStates() {
		for _, tr := range auth.Transitions() {
			want, allowed := table[from][tr]

			m := reach(t, from)
			next, err := apply(m, tr)

			switch {
			case allowed && err != nil:
				t.Errorf("%s + %s: unexpected error %v", from, tr, err)
			case allowed && next.State() != want:
				t.Errorf("%s + %s: reached %s, want %s", from, tr, next.State(), want)
			case !allowed && err == nil:
				t.Errorf("%s + %s: forbidden transition was accepted (now %s)", from, tr, next.State())
			case !allowed:
				assertTransitionError(t, err, from, tr)
			}
		}
	}
}

func assertTransitionError(t *testing.T, err error, from auth.State, tr auth.Transition) {
	t.Helper()

	if !errors.Is(err, auth.ErrInvalidTransition) {
		t.Errorf("%s + %s: error %v does not match ErrInvalidTransition", from, tr, err)
	}

	var te *auth.TransitionError
	if !errors.As(err, &te) {
		t.Fatalf("%s + %s: error %v is not a *TransitionError", from, tr, err)
	}
	if te.From != from || te.Transition != tr {
		t.Errorf("%s + %s: error reports from=%s transition=%s", from, tr, te.From, te.Transition)
	}
}

func TestMachineIsImmutable(t *testing.T) {
	created := auth.NewMachine()

	submitted, err := created.SubmitCredentials(auth.StrategyWidget)
	if err != nil {
		t.Fatalf("SubmitCredentials: %v", err)
	}
	if created.State() != auth.StateCreated {
		t.Fatalf("receiver mutated to %s", created.State())
	}
	if created.Strategy() != "" {
		t.Fatalf("receiver strategy mutated to %q", created.Strategy())
	}
	if submitted.Strategy() != auth.StrategyWidget {
		t.Fatalf("strategy = %q, want %q", submitted.Strategy(), auth.StrategyWidget)
	}

	pending, err := submitted.RequireMFA("sms")
	if err != nil {
		t.Fatalf("RequireMFA: %v", err)
	}
	if submitted.MFAMethod() != "" {
		t.Fatalf("receiver MFA method mutated to %q", submitted.MFAMethod())
	}
	if pending.MFAMethod() != "sms" {
		t.Fatalf("MFAMethod = %q, want sms", pending.MFAMethod())
	}
	if pending.Strategy() != auth.StrategyWidget {
		t.Fatalf("strategy lost across RequireMFA: %q", pending.Strategy())
	}
}

func TestStateTerminal(t *testing.T) {
	terminal := map[auth.State]bool{
		auth.StateAuthenticated: true,
		auth.StateFailed:        true,
		auth.StateExpired:       true,
		auth.StateCancelled:     true,
	}

	for _, state := range allStates() {
		if got := state.IsTerminal(); got != terminal[state] {
			t.Errorf("%s.IsTerminal() = %v, want %v", state, got, terminal[state])
		}
	}
}

func TestStateStringIsStableLabel(t *testing.T) {
	want := map[auth.State]string{
		auth.StateCreated:              "created",
		auth.StateCredentialsSubmitted: "credentials_submitted",
		auth.StateMFAPending:           "mfa_pending",
		auth.StateAuthenticated:        "authenticated",
		auth.StateFailed:               "failed",
		auth.StateExpired:              "expired",
		auth.StateCancelled:            "cancelled",
	}

	for state, label := range want {
		if got := state.String(); got != label {
			t.Errorf("State(%d).String() = %q, want %q", int(state), got, label)
		}
	}
	if got := auth.State(99).String(); got != "unknown" {
		t.Errorf("unknown state renders %q, want \"unknown\"", got)
	}
}

func TestStrategyFallbackOrder(t *testing.T) {
	want := []auth.StrategyName{auth.StrategyMobileIOS, auth.StrategyWidget, auth.StrategyPortal}

	got := auth.Strategies()
	if len(got) != len(want) {
		t.Fatalf("Strategies() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Strategies() = %v, want %v", got, want)
		}
	}

	got[0] = "tampered"
	if auth.Strategies()[0] != auth.StrategyMobileIOS {
		t.Fatal("Strategies() returned shared backing storage")
	}
}

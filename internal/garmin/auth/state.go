package auth

import (
	"errors"
	"slices"
)

// State is one node of the login state machine. The zero value is StateCreated,
// so a freshly declared Machine is usable.
type State int

// The login states. Source: the transition set required by the remote login
// design — created, credentials submitted, MFA pending, authenticated, plus the
// three terminal failure states.
const (
	// StateCreated is a login transaction that has not sent credentials yet.
	StateCreated State = iota
	// StateCredentialsSubmitted means Garmin accepted the credential POST and
	// has answered, but the transaction is not resolved yet.
	StateCredentialsSubmitted
	// StateMFAPending means Garmin demanded a one-time code.
	StateMFAPending
	// StateAuthenticated is the terminal success state: a DI token set exists.
	StateAuthenticated
	// StateFailed is the terminal state for a definitive failure.
	StateFailed
	// StateExpired is the terminal state for a transaction past its absolute TTL.
	StateExpired
	// StateCancelled is the terminal state for an explicitly abandoned login.
	StateCancelled
)

// stateLabels are stable snake_case labels safe for logs and metrics.
var stateLabels = map[State]string{
	StateCreated:              "created",
	StateCredentialsSubmitted: "credentials_submitted",
	StateMFAPending:           "mfa_pending",
	StateAuthenticated:        "authenticated",
	StateFailed:               "failed",
	StateExpired:              "expired",
	StateCancelled:            "cancelled",
}

// String returns the stable label, or "unknown" for a value that is not one of
// the package's State constants.
func (s State) String() string {
	if label, ok := stateLabels[s]; ok {
		return label
	}
	return labelUnknown
}

// IsTerminal reports whether no transition may leave s.
func (s State) IsTerminal() bool {
	switch s {
	case StateAuthenticated, StateFailed, StateExpired, StateCancelled:
		return true
	default:
		return false
	}
}

// Transition names one edge of the state machine. Each has exactly one method
// on Machine, so no caller can invent an edge.
type Transition string

// The transitions, one per Machine method.
const (
	// TransitionSubmitCredentials sends the credential POST for one strategy.
	TransitionSubmitCredentials Transition = "submit_credentials"
	// TransitionRequireMFA records that Garmin demanded a one-time code.
	TransitionRequireMFA Transition = "require_mfa"
	// TransitionAuthenticate accepts a session obtained without MFA.
	TransitionAuthenticate Transition = "authenticate"
	// TransitionVerifyMFA accepts a session obtained after a verified OTP.
	TransitionVerifyMFA Transition = "verify_mfa"
	// TransitionFail records a definitive failure.
	TransitionFail Transition = "fail"
	// TransitionExpire records that the absolute TTL elapsed.
	TransitionExpire Transition = "expire"
	// TransitionCancel abandons the transaction.
	TransitionCancel Transition = "cancel"
)

// transitions is the declaration order of the Transition constants.
var transitions = [...]Transition{
	TransitionSubmitCredentials,
	TransitionRequireMFA,
	TransitionAuthenticate,
	TransitionVerifyMFA,
	TransitionFail,
	TransitionExpire,
	TransitionCancel,
}

// Transitions returns a fresh copy of every transition the machine models.
func Transitions() []Transition {
	out := make([]Transition, len(transitions))
	copy(out, transitions[:])
	return out
}

// ErrInvalidTransition reports a transition the state machine forbids. Every
// rejection wraps it, so errors.Is(err, ErrInvalidTransition) is the general
// check and errors.As to *TransitionError is the specific one.
var ErrInvalidTransition = errors.New("garmin auth: invalid login state transition")

// TransitionError names the rejected edge. It carries no credential material:
// only two sanitized labels.
type TransitionError struct {
	// From is the state the machine was in.
	From State
	// Transition is the edge that was refused.
	Transition Transition
}

// Error renders the two labels and nothing else.
func (e *TransitionError) Error() string {
	return "garmin auth: transition " + string(e.Transition) +
		" is not permitted from state " + e.From.String()
}

// Is makes every TransitionError match ErrInvalidTransition.
func (e *TransitionError) Is(target error) bool { return target == ErrInvalidTransition }

// Machine is the immutable login state. Every transition returns a new Machine
// and leaves the receiver untouched, so a rejected transition cannot corrupt the
// caller's value and two concurrent logins cannot share mutable state.
//
// It holds no password and no OTP: those are request-scoped arguments that never
// reach a field.
type Machine struct {
	state     State
	strategy  StrategyName
	mfaMethod string
}

// NewMachine returns a Machine in StateCreated.
func NewMachine() Machine { return Machine{} }

// State reports the current state.
func (m Machine) State() State { return m.state }

// Strategy reports the strategy that submitted credentials, or "" before that.
func (m Machine) Strategy() StrategyName { return m.strategy }

// MFAMethod reports the delivery method Garmin named, or "" when no MFA
// challenge was seen. It is server-controlled text, kept for display only.
func (m Machine) MFAMethod() string { return m.mfaMethod }

// SubmitCredentials moves created to credentials submitted, recording which
// strategy is in flight.
func (m Machine) SubmitCredentials(strategy StrategyName) (Machine, error) {
	if err := m.require(TransitionSubmitCredentials, StateCreated); err != nil {
		return m, err
	}
	next := m
	next.state = StateCredentialsSubmitted
	next.strategy = strategy
	return next, nil
}

// RequireMFA moves credentials submitted to MFA pending.
func (m Machine) RequireMFA(method string) (Machine, error) {
	if err := m.require(TransitionRequireMFA, StateCredentialsSubmitted); err != nil {
		return m, err
	}
	next := m
	next.state = StateMFAPending
	next.mfaMethod = method
	return next, nil
}

// Authenticate moves credentials submitted to authenticated, for a login that
// needed no OTP.
func (m Machine) Authenticate() (Machine, error) {
	return m.to(TransitionAuthenticate, StateAuthenticated, StateCredentialsSubmitted)
}

// VerifyMFA moves MFA pending to authenticated. It is the single-use terminal
// transition of an MFA continuation.
func (m Machine) VerifyMFA() (Machine, error) {
	return m.to(TransitionVerifyMFA, StateAuthenticated, StateMFAPending)
}

// Fail moves any non-terminal state to failed.
func (m Machine) Fail() (Machine, error) {
	return m.to(TransitionFail, StateFailed, StateCreated, StateCredentialsSubmitted, StateMFAPending)
}

// Expire moves any non-terminal state to expired.
func (m Machine) Expire() (Machine, error) {
	return m.to(TransitionExpire, StateExpired, StateCreated, StateCredentialsSubmitted, StateMFAPending)
}

// Cancel moves any non-terminal state to cancelled.
func (m Machine) Cancel() (Machine, error) {
	return m.to(TransitionCancel, StateCancelled, StateCreated, StateCredentialsSubmitted, StateMFAPending)
}

// to applies tr when the current state is one of from, returning a new Machine.
func (m Machine) to(tr Transition, target State, from ...State) (Machine, error) {
	if err := m.require(tr, from...); err != nil {
		return m, err
	}
	next := m
	next.state = target
	return next, nil
}

// require reports a *TransitionError unless the current state is one of allowed.
func (m Machine) require(tr Transition, allowed ...State) error {
	if slices.Contains(allowed, m.state) {
		return nil
	}
	return &TransitionError{From: m.state, Transition: tr}
}

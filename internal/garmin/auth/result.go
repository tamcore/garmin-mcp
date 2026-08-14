package auth

import (
	"encoding/json"
	"log/slog"
)

// Result is the outcome of a Login or CompleteMFA call.
//
// It is secret-bearing: on an MFA challenge it carries the transaction
// capability, which is a bearer credential for the pending login. Like TokenSet
// it hides its material behind a pointer to unexported fields, and String,
// GoString, MarshalJSON and LogValue report only the shape. The capability must
// travel to the browser in a short-lived host-only cookie, never in a path, a
// query string or a log line.
type Result struct {
	// secrets is a pointer on purpose; see TokenSet.secrets.
	secrets *resultSecrets
}

// resultSecrets is the sealed content of a Result. The transaction capability is a
// bearer credential, so it sits behind a pointer; see secretString.
type resultSecrets struct {
	state                State
	strategy             StrategyName
	transactionID        *secretString
	mfaMethod            string
	mfaDeliveryUncertain bool
}

// authenticatedResult reports a completed login.
func authenticatedResult(strategy StrategyName) Result {
	return Result{secrets: &resultSecrets{state: StateAuthenticated, strategy: strategy}}
}

// mfaPendingResult reports a login waiting for a one-time code.
func mfaPendingResult(strategy StrategyName, transactionID string, pending Pending) Result {
	return Result{secrets: &resultSecrets{
		state:                StateMFAPending,
		strategy:             strategy,
		transactionID:        sealSecret(transactionID),
		mfaMethod:            pending.MFAMethod(),
		mfaDeliveryUncertain: pending.MFADeliveryUncertain(),
	}}
}

// failedResult reports a login that will not continue.
func failedResult(strategy StrategyName) Result {
	return Result{secrets: &resultSecrets{state: StateFailed, strategy: strategy}}
}

func (r Result) s() resultSecrets {
	if r.secrets == nil {
		return resultSecrets{}
	}
	return *r.secrets
}

// State is the login state the transaction reached.
func (r Result) State() State { return r.s().state }

// Strategy is the strategy that produced this outcome, or "" when none ran.
func (r Result) Strategy() StrategyName { return r.s().strategy }

// NeedsMFA reports whether a one-time code must be submitted with CompleteMFA.
func (r Result) NeedsMFA() bool { return r.s().state == StateMFAPending }

// TransactionID is the opaque capability for the pending MFA transaction, or ""
// when no continuation is pending. It is a credential: hand it to the browser in
// a host-only cookie and never log it.
func (r Result) TransactionID() string { return revealSecret(r.s().transactionID) }

// MFAMethod is the delivery method Garmin named for the challenge.
func (r Result) MFAMethod() string { return r.s().mfaMethod }

// MFADeliveryUncertain reports a challenge scraped from HTML, where code
// delivery is not confirmed.
func (r Result) MFADeliveryUncertain() bool { return r.s().mfaDeliveryUncertain }

// redactedResult is the only shape a Result is ever rendered in.
type redactedResult struct {
	Type           string `json:"type"`
	State          string `json:"state"`
	Strategy       string `json:"strategy"`
	MFAMethod      string `json:"mfaMethod,omitempty"`
	HasTransaction bool   `json:"transactionPresent"`
}

func (r Result) redacted() redactedResult {
	secrets := r.s()
	return redactedResult{
		Type:           "auth.Result",
		State:          secrets.state.String(),
		Strategy:       secrets.strategy.String(),
		MFAMethod:      knownMFAMethod(secrets.mfaMethod),
		HasTransaction: secrets.transactionID != nil,
	}
}

// String renders a Result without its transaction capability.
func (r Result) String() string {
	red := r.redacted()
	return "auth.Result{state:" + red.State +
		" strategy:" + red.Strategy +
		" mfaMethod:" + quoteLabel(red.MFAMethod) +
		" transaction:" + presence(red.HasTransaction) + "}"
}

// GoString satisfies the %#v verb with the same redacted rendering.
func (r Result) GoString() string { return r.String() }

// MarshalJSON serializes the redacted form.
func (r Result) MarshalJSON() ([]byte, error) { return json.Marshal(r.redacted()) }

// LogValue implements slog.LogValuer.
func (r Result) LogValue() slog.Value {
	red := r.redacted()
	return slog.GroupValue(
		slog.String("type", red.Type),
		slog.String("state", red.State),
		slog.String("strategy", red.Strategy),
		slog.String("mfaMethod", red.MFAMethod),
		slog.Bool("transactionPresent", red.HasTransaction),
	)
}

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

// resultSecrets is the sealed content of a Result. The transaction capability and
// the Garmin account both sit behind pointers; see secretString.
type resultSecrets struct {
	state                State
	strategy             StrategyName
	transactionID        *secretString
	mfaMethod            string
	mfaDeliveryUncertain bool
	accountID            *secretString
	displayName          *secretString
}

// garminAccount is the identity Garmin reported for a session this package
// validated.
//
// The account id is not a credential, but it is a stable, guessable,
// account-scoped identifier — the value an attacker would want in order to
// correlate users — so it is treated exactly like one on the way out: sealed,
// reported only as presence, and reachable only through the accessor a caller
// asked for deliberately. internal/store keeps it the same way, as a keyed HMAC
// and an AEAD envelope, never in the clear.
type garminAccount struct {
	// accountID is Garmin's stable account identifier, or "" when the profile
	// named none.
	accountID string

	// displayName is what Garmin reports as the account's name. It is unverified
	// remote text, so a caller must escape it before rendering.
	displayName string
}

// authenticatedResult reports a completed login for the account Garmin confirmed.
func authenticatedResult(strategy StrategyName, account garminAccount) Result {
	return Result{secrets: &resultSecrets{
		state:       StateAuthenticated,
		strategy:    strategy,
		accountID:   sealSecret(account.accountID),
		displayName: sealSecret(account.displayName),
	}}
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

// GarminAccountID is Garmin's stable identifier for the account this login
// authenticated, or "" when the login did not complete.
//
// It is the value a multi-user deployment keys a principal on, because an email is
// a login handle that its owner can change and that two people can dispute. It must
// not be logged, and a caller that stores it must store a keyed HMAC or an
// encrypted association rather than the identifier itself.
func (r Result) GarminAccountID() string { return revealSecret(r.s().accountID) }

// GarminDisplayName is what Garmin reports as the account's name, or "" when the
// login did not complete or Garmin named none. It is unverified remote text, so a
// caller must escape it before rendering.
func (r Result) GarminDisplayName() string { return revealSecret(r.s().displayName) }

// redactedResult is the only shape a Result is ever rendered in.
type redactedResult struct {
	Type           string `json:"type"`
	State          string `json:"state"`
	Strategy       string `json:"strategy"`
	MFAMethod      string `json:"mfaMethod,omitempty"`
	HasTransaction bool   `json:"transactionPresent"`
	HasAccount     bool   `json:"garminAccountPresent"`
}

func (r Result) redacted() redactedResult {
	secrets := r.s()
	return redactedResult{
		Type:           "auth.Result",
		State:          secrets.state.String(),
		Strategy:       secrets.strategy.String(),
		MFAMethod:      knownMFAMethod(secrets.mfaMethod),
		HasTransaction: secrets.transactionID != nil,
		HasAccount:     secrets.accountID != nil,
	}
}

// String renders a Result without its transaction capability.
func (r Result) String() string {
	red := r.redacted()
	return "auth.Result{state:" + red.State +
		" strategy:" + red.Strategy +
		" mfaMethod:" + quoteLabel(red.MFAMethod) +
		" transaction:" + presence(red.HasTransaction) +
		" garminAccount:" + presence(red.HasAccount) + "}"
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
		slog.Bool("garminAccountPresent", red.HasAccount),
	)
}

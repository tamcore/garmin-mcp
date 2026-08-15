package tools

import (
	"context"
	"errors"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
	"github.com/tamcore/garmin-mcp/internal/identity"
)

// Sentinel errors for errors.Is checks. Each names one failure class and carries no
// request detail.
var (
	// ErrMissingDependency reports a Deps value that cannot be used.
	ErrMissingDependency = errors.New("garmin tools: incomplete dependencies")

	// ErrUnknownTierTool reports a tier list that names a tool which is not
	// registered. It is the typo guard: it fails at start-up, not at call time.
	ErrUnknownTierTool = errors.New("garmin tools: a tier list names an unregistered tool")

	// ErrUntieredTool reports a registered tool that appears in no tier list, which
	// would leave it without a policy tier and therefore refused at call time.
	ErrUntieredTool = errors.New("garmin tools: a registered tool appears in no tier list")

	// ErrInvalidArgument reports a tool argument this package rejected before any
	// Garmin call could have an effect.
	ErrInvalidArgument = errors.New("garmin tools: invalid tool argument")

	// ErrResultTooLarge reports a result over its configured bound.
	ErrResultTooLarge = errors.New("garmin tools: result exceeds its bound")

	// ErrIncompleteProfile reports a Garmin profile that does not carry the field
	// the tool exists to return. It is a real state for a new account.
	ErrIncompleteProfile = errors.New("garmin tools: the Garmin profile is incomplete")
)

// AdviceNoSuchRecord is the advice a call for something Garmin does not hold comes
// back with.
//
// It is exported because it is the only signal a caller has for that one class. A tool
// result carries authored advice and nothing else — deliberately, since a class name or
// a status would be this server's internals — so a caller that must tell "the record is
// gone" from "the call failed" has this sentence and no alternative. Anything reading
// it is asserting on a constant rather than on prose, and a reworded sentence moves
// both sides at once.
const AdviceNoSuchRecord = "Garmin holds no such record for this account."

// A ToolError is the caller-facing failure of one tool call.
//
// Error returns only the authored advice. That is the whole point: the SDK puts the
// handler's error text into the tool result the model reads, so anything the message
// carried would leave this process. A cause is still reachable through Unwrap, so
// errors.Is and errors.As keep working for a caller that needs the class.
type ToolError struct {
	// Advice is the caller-facing remediation. It is authored text, never a
	// rendered payload, and it names no value the caller supplied.
	Advice string

	// Err is the wrapped cause. It is never rendered.
	Err error
}

// Error returns the authored advice.
func (e *ToolError) Error() string {
	if e == nil {
		return ""
	}
	return e.Advice
}

// Unwrap exposes the wrapped cause.
func (e *ToolError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// fail wraps a cause in the advice its class deserves.
func fail(err error) error {
	return &ToolError{Advice: advise(err), Err: err}
}

// invalidArgument refuses an argument. reason must be authored text: it is rendered
// verbatim, so it may never quote what the caller sent.
func invalidArgument(reason string) error {
	return &ToolError{
		Advice: "This tool refused the arguments before any Garmin call: " + reason + ".",
		Err:    ErrInvalidArgument,
	}
}

// tooLarge refuses a result that outgrew its bound, and says how to ask for less.
func tooLarge(reason string) error {
	return &ToolError{
		Advice: "The result exceeds this server's bound: " + reason + ".",
		Err:    ErrResultTooLarge,
	}
}

// incompleteProfile reports a profile field Garmin does not carry for this account.
func incompleteProfile(reason string) error {
	return &ToolError{
		Advice: "The Garmin profile is incomplete: " + reason + ".",
		Err:    ErrIncompleteProfile,
	}
}

// advise maps a failure class onto an actionable sentence.
//
// The sentences are the whole caller-facing vocabulary of this package. None quotes a
// response body, a header, a URL with a query, a coordinate or a Go value.
func advise(err error) string {
	if advice, ok := adviseLocal(err); ok {
		return advice
	}
	if advice, ok := adviseUpstream(err); ok {
		return advice
	}
	return "The Garmin read failed for a reason this server could not classify. " +
		"Retry once; if it persists, check the server logs."
}

// adviseLocal covers the classes this server decides on its own.
func adviseLocal(err error) (string, bool) {
	switch {
	case errors.Is(err, identity.ErrNoPrincipal), errors.Is(err, client.ErrMissingPrincipal):
		return "This request could not be attributed to an account, so it was refused.", true
	case errors.Is(err, ErrInvalidArgument), errors.Is(err, client.ErrValidation):
		return "The arguments were rejected as invalid before they reached Garmin. " +
			"Check the date format, the ranges and the identifier.", true
	case errors.Is(err, ErrResultTooLarge), errors.Is(err, client.ErrResponseTooLarge):
		return "The result exceeds this server's bound. Narrow the date window or " +
			"ask for a smaller page.", true
	case errors.Is(err, client.ErrPaginationExhausted):
		return "Garmin kept returning full pages past this server's page bound. " +
			"Narrow the date window and try again.", true
	case errors.Is(err, context.Canceled):
		return "The call was cancelled before Garmin answered.", true
	case errors.Is(err, context.DeadlineExceeded):
		return "The call timed out before Garmin answered. Retry with a smaller request.", true
	}
	return "", false
}

// adviseUpstream covers the classes Garmin decides.
func adviseUpstream(err error) (string, bool) {
	switch {
	case errors.Is(err, client.ErrAuthentication):
		return "Garmin rejected the session for this account. Re-authenticate the " +
			"account, then retry.", true
	case errors.Is(err, client.ErrRateLimited):
		return "Garmin rate-limited this account. Wait for the retry window to pass " +
			"before calling again.", true
	case errors.Is(err, client.ErrNotFound):
		return AdviceNoSuchRecord, true
	case errors.Is(err, client.ErrServer), errors.Is(err, client.ErrTemporaryConnection):
		return "Garmin is temporarily unavailable. Retry in a moment.", true
	case errors.Is(err, client.ErrMalformedPayload), errors.Is(err, client.ErrUnexpectedResponse):
		return "Garmin returned a response this server could not interpret. This is " +
			"usually upstream drift; retrying rarely helps.", true
	case errors.Is(err, ErrIncompleteProfile):
		return "The Garmin profile does not carry this field. Set it in Garmin Connect.", true
	}
	return "", false
}

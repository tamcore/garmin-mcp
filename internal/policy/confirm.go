package policy

import (
	"context"
	"errors"
	"fmt"
)

// A ConfirmationRequest describes the operation a user is being asked to confirm.
//
// Summary is shown to the user, so it must describe the effect in plain words and
// must never carry Garmin payload, health data, or a coordinate.
type ConfirmationRequest struct {
	Tool    string
	Tier    Tier
	Summary string
}

// A Confirmer asks the user to confirm a destructive operation.
//
// The runtime implementation is backed by MCP elicitation over the client
// session. It must return one of ErrConfirmationUnsupported,
// ErrConfirmationDeclined, or ErrConfirmationTimedOut where it can tell them
// apart, so that the refusal can name the reason.
type Confirmer interface {
	Confirm(ctx context.Context, req ConfirmationRequest) error
}

// RequireConfirmation obtains confirmation for req and returns nil only when the
// user affirmatively confirmed.
//
// It fails closed. This is a deliberate deviation from the house kubectl-mcp
// behavior, which proceeds when elicitation is unsupported or times out: here a
// destructive Garmin operation that cannot obtain confirmation is refused, and
// the returned error names why. Every refusal wraps ErrConfirmationRequired as
// well as the specific reason, so a caller can test for either.
//
// A nil Confirmer means the deployment has no way to ask, which is treated as
// unsupported and therefore as a refusal — never as consent.
func RequireConfirmation(ctx context.Context, confirmer Confirmer, req ConfirmationRequest) error {
	if !req.Tier.RequiresConfirmation() {
		return fmt.Errorf("tier %s does not require confirmation: %w",
			req.Tier, ErrConfirmationNotApplicable)
	}
	if confirmer == nil {
		return refusal(ErrConfirmationUnsupported)
	}

	if err := confirmer.Confirm(ctx, req); err != nil {
		return classifyConfirmationError(ctx, err)
	}
	return nil
}

// classifyConfirmationError maps a Confirmer failure onto one reason.
//
// The original error is never wrapped. It comes from the client session and may
// carry an Authorization header, a cookie, or a response body, none of which may
// reach a caller-facing refusal or a log line.
func classifyConfirmationError(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, ErrConfirmationUnsupported):
		return refusal(ErrConfirmationUnsupported)
	case errors.Is(err, ErrConfirmationDeclined):
		return refusal(ErrConfirmationDeclined)
	case errors.Is(err, ErrConfirmationTimedOut), errors.Is(err, context.DeadlineExceeded):
		return refusal(ErrConfirmationTimedOut)
	case errors.Is(err, context.Canceled), ctx.Err() != nil:
		// Cancellation has no sentinel of its own: it is not a client decision
		// and not a timeout. It still refuses, and the word is spelled out
		// because no wrapped sentinel supplies it.
		return fmt.Errorf("confirmation was cancelled before the user answered: %w",
			ErrConfirmationRequired)
	default:
		return refusal(ErrConfirmationUnavailable)
	}
}

// refusal wraps both the general and the specific sentinel. The specific
// sentinel's own text supplies the reason word, so the message cannot drift out
// of step with the error a caller matches on.
func refusal(reason error) error {
	return fmt.Errorf("%w: %w", ErrConfirmationRequired, reason)
}

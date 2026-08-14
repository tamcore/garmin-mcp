package policy_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tamcore/garmin-mcp/internal/policy"
)

// confirmerFunc adapts a function to policy.Confirmer.
type confirmerFunc func(context.Context, policy.ConfirmationRequest) error

func (f confirmerFunc) Confirm(ctx context.Context, req policy.ConfirmationRequest) error {
	return f(ctx, req)
}

func destructiveRequest() policy.ConfirmationRequest {
	return policy.ConfirmationRequest{
		Tool:    destructiveTool,
		Tier:    policy.TierDestructive,
		Summary: "permanently delete one stored workout",
	}
}

func TestRequireConfirmationAcceptsAnAffirmativeConfirmation(t *testing.T) {
	t.Parallel()

	var seen policy.ConfirmationRequest
	confirmer := confirmerFunc(func(_ context.Context, req policy.ConfirmationRequest) error {
		seen = req
		return nil
	})

	if err := policy.RequireConfirmation(context.Background(), confirmer, destructiveRequest()); err != nil {
		t.Fatalf("RequireConfirmation returned error for an accepted confirmation: %v", err)
	}
	if seen.Tool != destructiveTool {
		t.Fatalf("the confirmer saw Tool = %q, want %q", seen.Tool, destructiveTool)
	}
	if seen.Summary == "" {
		t.Fatal("the confirmer must be told what it is confirming")
	}
}

// This is the deviation from the house server: no confirmer means the operation
// is refused, not performed.
func TestRequireConfirmationFailsClosedWhenUnsupported(t *testing.T) {
	t.Parallel()

	err := policy.RequireConfirmation(context.Background(), nil, destructiveRequest())
	if !errors.Is(err, policy.ErrConfirmationUnsupported) {
		t.Fatalf("RequireConfirmation(nil confirmer) error = %v, want ErrConfirmationUnsupported", err)
	}
	if !errors.Is(err, policy.ErrConfirmationRequired) {
		t.Fatalf("error = %v, want it to also wrap ErrConfirmationRequired", err)
	}
	assertNamesReason(t, err, "unsupported")
}

func TestRequireConfirmationFailsClosedWhenTheClientReportsNoSupport(t *testing.T) {
	t.Parallel()

	confirmer := confirmerFunc(func(context.Context, policy.ConfirmationRequest) error {
		return policy.ErrConfirmationUnsupported
	})

	err := policy.RequireConfirmation(context.Background(), confirmer, destructiveRequest())
	if !errors.Is(err, policy.ErrConfirmationUnsupported) {
		t.Fatalf("error = %v, want ErrConfirmationUnsupported", err)
	}
	assertNamesReason(t, err, "unsupported")
}

func TestRequireConfirmationFailsClosedWhenDeclined(t *testing.T) {
	t.Parallel()

	confirmer := confirmerFunc(func(context.Context, policy.ConfirmationRequest) error {
		return policy.ErrConfirmationDeclined
	})

	err := policy.RequireConfirmation(context.Background(), confirmer, destructiveRequest())
	if !errors.Is(err, policy.ErrConfirmationDeclined) {
		t.Fatalf("error = %v, want ErrConfirmationDeclined", err)
	}
	if !errors.Is(err, policy.ErrConfirmationRequired) {
		t.Fatalf("error = %v, want it to also wrap ErrConfirmationRequired", err)
	}
	assertNamesReason(t, err, "declined")
}

func TestRequireConfirmationFailsClosedWhenTimedOut(t *testing.T) {
	t.Parallel()

	confirmer := confirmerFunc(func(context.Context, policy.ConfirmationRequest) error {
		return policy.ErrConfirmationTimedOut
	})

	err := policy.RequireConfirmation(context.Background(), confirmer, destructiveRequest())
	if !errors.Is(err, policy.ErrConfirmationTimedOut) {
		t.Fatalf("error = %v, want ErrConfirmationTimedOut", err)
	}
	assertNamesReason(t, err, "timed out")
}

// A deadline that expires while the confirmer is waiting is a timeout, not an
// unclassified transport failure.
func TestRequireConfirmationTranslatesADeadlineIntoATimeout(t *testing.T) {
	t.Parallel()

	confirmer := confirmerFunc(func(ctx context.Context, _ policy.ConfirmationRequest) error {
		<-ctx.Done()
		return ctx.Err()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err := policy.RequireConfirmation(ctx, confirmer, destructiveRequest())
	if !errors.Is(err, policy.ErrConfirmationTimedOut) {
		t.Fatalf("error = %v, want ErrConfirmationTimedOut", err)
	}
	assertNamesReason(t, err, "timed out")
}

func TestRequireConfirmationTreatsCancellationAsAFailureToConfirm(t *testing.T) {
	t.Parallel()

	confirmer := confirmerFunc(func(ctx context.Context, _ policy.ConfirmationRequest) error {
		<-ctx.Done()
		return ctx.Err()
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := policy.RequireConfirmation(ctx, confirmer, destructiveRequest())
	if !errors.Is(err, policy.ErrConfirmationRequired) {
		t.Fatalf("error = %v, want ErrConfirmationRequired", err)
	}
	assertNamesReason(t, err, "cancelled")
}

// An unclassified confirmer failure must still refuse, and must not leak the
// transport's own error text into a caller-facing reason.
func TestRequireConfirmationFailsClosedOnAnUnclassifiedError(t *testing.T) {
	t.Parallel()

	confirmer := confirmerFunc(func(context.Context, policy.ConfirmationRequest) error {
		return errors.New("session write failed: bearer abc123")
	})

	err := policy.RequireConfirmation(context.Background(), confirmer, destructiveRequest())
	if !errors.Is(err, policy.ErrConfirmationUnavailable) {
		t.Fatalf("error = %v, want ErrConfirmationUnavailable", err)
	}
	if strings.Contains(err.Error(), "bearer abc123") {
		t.Fatalf("error %q echoes the transport error text", err)
	}
	assertNamesReason(t, err, "unavailable")
}

// A non-destructive request has no business asking for confirmation; that would
// mean the caller wired the middleware wrongly.
func TestRequireConfirmationRejectsANonDestructiveRequest(t *testing.T) {
	t.Parallel()

	req := destructiveRequest()
	req.Tier = policy.TierReadOnly

	confirmer := confirmerFunc(func(context.Context, policy.ConfirmationRequest) error { return nil })

	err := policy.RequireConfirmation(context.Background(), confirmer, req)
	if !errors.Is(err, policy.ErrConfirmationNotApplicable) {
		t.Fatalf("error = %v, want ErrConfirmationNotApplicable", err)
	}
}

// assertNamesReason checks the refusal names why confirmation could not be
// obtained, which the brief requires explicitly.
func assertNamesReason(t *testing.T, err error, reason string) {
	t.Helper()

	if err == nil {
		t.Fatal("expected a refusal")
	}
	if !strings.Contains(err.Error(), reason) {
		t.Fatalf("refusal %q does not name the reason %q", err, reason)
	}
}

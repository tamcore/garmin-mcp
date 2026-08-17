package protocol

import "testing"

func TestOutcomeString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		outcome Outcome
		want    string
	}{
		{OutcomeUnknown, "unknown"},
		{OutcomeSuccess, "success"},
		{OutcomeMFARequired, "mfa_required"},
		{OutcomeInvalidCredentials, "invalid_credentials"},
		{OutcomeAccountLocked, "account_locked"},
		{OutcomeAccountRestricted, "account_restricted"},
		{OutcomeSessionRejected, "session_rejected"},
		{OutcomeMFARejected, "mfa_rejected"},
		{OutcomeBotChallenge, "bot_challenge"},
		{OutcomeRateLimited, "rate_limited"},
		{OutcomeTemporaryFailure, "temporary_failure"},
		{Outcome(99), "invalid_outcome(99)"},
	}

	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			if got := tc.outcome.String(); got != tc.want {
				t.Fatalf("String() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestOutcomeRetryableAndFallback(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		outcome       Outcome
		wantRetryable bool
		wantStops     bool
	}{
		{name: "success stops fallback", outcome: OutcomeSuccess, wantRetryable: false, wantStops: true},
		{name: "mfa stops fallback", outcome: OutcomeMFARequired, wantRetryable: false, wantStops: true},
		{
			name: "invalid credentials stops fallback", outcome: OutcomeInvalidCredentials,
			wantRetryable: false, wantStops: true,
		},
		{name: "account locked stops fallback", outcome: OutcomeAccountLocked, wantRetryable: false, wantStops: true},
		{name: "restricted account continues", outcome: OutcomeAccountRestricted, wantRetryable: false, wantStops: false},
		{
			name: "session rejected continues", outcome: OutcomeSessionRejected,
			wantRetryable: false, wantStops: false,
		},
		{name: "bot challenge continues", outcome: OutcomeBotChallenge, wantRetryable: false, wantStops: false},
		{
			// Never seen during Login's strategy fallback: it is produced only by
			// the MFA-verify-specific classifiers.
			name: "mfa rejected does not stop fallback", outcome: OutcomeMFARejected,
			wantRetryable: false, wantStops: false,
		},
		{name: "rate limited retryable", outcome: OutcomeRateLimited, wantRetryable: true, wantStops: false},
		{name: "temporary retryable", outcome: OutcomeTemporaryFailure, wantRetryable: true, wantStops: false},
		{name: "unknown neither", outcome: OutcomeUnknown, wantRetryable: false, wantStops: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.outcome.Retryable(); got != tc.wantRetryable {
				t.Fatalf("Retryable() = %v, want %v", got, tc.wantRetryable)
			}
			if got := tc.outcome.StopsFallback(); got != tc.wantStops {
				t.Fatalf("StopsFallback() = %v, want %v", got, tc.wantStops)
			}
		})
	}
}

package policy

import "errors"

// Configuration errors. New and Validate return these, always at start-up, so a
// misconfiguration never becomes a runtime surprise.
var (
	// ErrInvalidMode reports that Config.Mode is not ModeLocal or ModeRemote.
	// There is no default mode: read-only-by-default for remote deployments only
	// means anything if the deployment says which one it is.
	ErrInvalidMode = errors.New("policy: invalid deployment mode")

	// ErrInvalidToolName reports an empty or whitespace-only entry in a tier
	// list, an allowlist, or a denylist.
	ErrInvalidToolName = errors.New("policy: invalid tool name")

	// ErrDuplicateTool reports that one tool name appears in more than one tier
	// list. A tool has exactly one tier.
	ErrDuplicateTool = errors.New("policy: tool named in more than one tier")

	// ErrToolNotRegistered reports that a configured name does not exist in the
	// actually-registered tool set. This is what turns a typo into a start-up
	// failure instead of a tool that silently never matches.
	ErrToolNotRegistered = errors.New("policy: configured tool is not registered")

	// ErrToolWithoutTier reports that a registered tool appears in no tier list.
	// Such a tool would be permanently unreachable, which is a configuration bug
	// rather than an intentional denial.
	ErrToolWithoutTier = errors.New("policy: registered tool has no tier")
)

// Decision errors. Decision.Err wraps exactly one of these, so a caller can
// branch on the cause with errors.Is while showing Decision.Reason to the user.
var (
	// ErrUnknownTool reports a call to a tool this policy has no tier for.
	ErrUnknownTool = errors.New("policy: unknown tool")

	// ErrToolDenied reports that the operator denylisted the tool. The denylist
	// is evaluated before the tier gate, so it beats enablement and scope.
	ErrToolDenied = errors.New("policy: tool is denylisted")

	// ErrNotAllowlisted reports that a non-empty allowlist does not name the
	// tool.
	ErrNotAllowlisted = errors.New("policy: tool is not allowlisted")

	// ErrTierNotEnabled reports that the operator did not enable the tool's
	// tier. Remote deployments start with both the write and the destructive
	// tier disabled, which is what "remote defaults to read-only" means here.
	ErrTierNotEnabled = errors.New("policy: tool tier is not enabled by the operator")

	// ErrScopeNotGranted reports that the caller holds no OAuth scope for the
	// tool's tier. Only the remote transport can present one, so on stdio this
	// refuses every write and destructive tool regardless of enablement.
	ErrScopeNotGranted = errors.New("policy: required OAuth scope is not granted")

	// ErrScopeLookupFailed reports that the granted scopes could not be
	// determined. It fails closed: an unknown grant is not a grant.
	ErrScopeLookupFailed = errors.New("policy: granted scopes could not be determined")
)

// Confirmation errors. Every failure to obtain confirmation wraps
// ErrConfirmationRequired plus the specific reason, so a caller can test for
// "refused for want of confirmation" without enumerating the reasons.
var (
	// ErrConfirmationRequired reports that a destructive tool was refused
	// because confirmation was required and not obtained.
	ErrConfirmationRequired = errors.New("policy: destructive tool requires confirmation")

	// ErrConfirmationUnsupported reports that the client cannot be asked. Unlike
	// the house implementation, which proceeds, this refuses.
	ErrConfirmationUnsupported = errors.New("policy: confirmation is unsupported by the client")

	// ErrConfirmationDeclined reports that the user declined or dismissed.
	ErrConfirmationDeclined = errors.New("policy: confirmation was declined")

	// ErrConfirmationTimedOut reports that the bounded wait elapsed.
	ErrConfirmationTimedOut = errors.New("policy: confirmation timed out")

	// ErrConfirmationUnavailable reports an unclassified failure to ask. The
	// underlying error text is deliberately not propagated: it comes from the
	// transport and may carry header or token material.
	ErrConfirmationUnavailable = errors.New("policy: confirmation is unavailable")

	// ErrConfirmationNotApplicable reports that confirmation was requested for a
	// tier that does not need it, which means the caller wired the middleware
	// wrongly.
	ErrConfirmationNotApplicable = errors.New("policy: confirmation does not apply to this tier")
)

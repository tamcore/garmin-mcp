package mcpserver

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/policy"
)

// protocolVersionMultiRoundTrip is the first MCP version that forbids a
// server-to-client request while a request is being served.
//
// This is the version at which SEP-2322 replaced in-flight elicitation with the
// multi-round-trip input-request mechanism. Protocol versions are ISO dates, so an
// ordinary string comparison orders them, which is how the SDK compares them too.
const protocolVersionMultiRoundTrip = "2026-07-28"

// confirmationProperty is the single boolean the user answers.
const confirmationProperty = "confirm"

// confirmationKeyPrefix namespaces the input-request key.
//
// The key carries the tool name, and the retry looks up that exact key. A
// confirmation the user gave for one tool therefore cannot be replayed to authorize
// a different one, because the response would be filed under a key this tool never
// asks for.
const confirmationKeyPrefix = "confirm:"

func confirmationKey(tool string) string { return confirmationKeyPrefix + tool }

// confirmationOutcome is what the confirmation step decided.
type confirmationOutcome int

const (
	// confirmationGranted means the user confirmed and the tool may run.
	confirmationGranted confirmationOutcome = iota + 1

	// confirmationPending means the client must be asked. The caller returns the
	// accompanying input-required result and the tool does not run yet.
	confirmationPending

	// confirmationRefused means the tool must not run. The accompanying error
	// names why.
	confirmationRefused
)

// confirmDestructive decides whether a destructive tool may proceed.
//
// It fails closed, which is the deliberate deviation from the house kubectl-mcp
// server: a client that cannot be asked, a user who declines, a user who dismisses,
// and a wait that elapses all refuse the operation, and the refusal names the reason.
//
// Two protocol shapes are handled, because the SDK behaves differently across the
// versions it negotiates:
//
//   - At 2026-07-28 and later, SEP-2322 forbids sending elicitation/create while a
//     request is in flight, and the SDK enforces it. The server instead answers the
//     call with an input-required result carrying an ElicitParams, and the client
//     re-calls the tool with the user's answer.
//   - Before 2026-07-28, an in-flight server-to-client request is permitted, so the
//     session is asked directly.
//
// The policy gate re-runs from scratch on the retry, so the round trip grants
// nothing on its own: RequestState is a correlation hint, never a capability.
func (s *Server) confirmDestructive(ctx context.Context, req mcp.Request, decision policy.Decision) (
	confirmationOutcome, *mcp.CallToolResult, error,
) {
	callReq, ok := req.(*mcp.CallToolRequest)
	if !ok {
		return confirmationRefused, nil, refuseConfirmation(policy.ErrConfirmationUnsupported)
	}

	// An answer already on this request means the client has been round-tripped.
	if answer, present := lookupConfirmation(callReq, decision.Tool); present {
		if err := interpretElicitResult(answer); err != nil {
			return confirmationRefused, nil, refuseConfirmation(err)
		}
		return confirmationGranted, nil, nil
	}

	if !clientDeclaresElicitation(callReq) {
		return confirmationRefused, nil, refuseConfirmation(policy.ErrConfirmationUnsupported)
	}

	params := s.elicitParams(decision)
	if callReq.ProtocolVersion() >= protocolVersionMultiRoundTrip {
		return confirmationPending, inputRequiredResult(decision.Tool, params), nil
	}

	// Older protocol versions still permit an in-flight server-to-client request.
	result, err := callReq.Session.Elicit(ctx, params)
	if err != nil {
		return confirmationRefused, nil, refuseConfirmation(classifyElicitError(ctx, err))
	}
	if err := interpretElicitResult(result); err != nil {
		return confirmationRefused, nil, refuseConfirmation(err)
	}
	return confirmationGranted, nil, nil
}

// elicitParams builds the confirmation prompt.
//
// The message is assembled from the tool's own description and category, both
// authored in this repository. A tool argument never appears: an argument can carry
// a Garmin identifier or a health value that has no place in a prompt.
func (s *Server) elicitParams(decision policy.Decision) *mcp.ElicitParams {
	return &mcp.ElicitParams{
		Mode: "form",
		Message: "Confirm a " + decision.Tier.String() + " operation: " +
			confirmationSummary(s.registry, decision.Tool),
		RequestedSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				confirmationProperty: map[string]any{
					"type":        "boolean",
					"description": "Set true to allow this operation to proceed.",
				},
			},
		},
	}
}

// inputRequiredResult asks the client for confirmation.
//
// Content is deliberately left unset: the SDK rejects a result that carries both
// content and input requests.
func inputRequiredResult(tool string, params *mcp.ElicitParams) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		InputRequests: mcp.InputRequestMap{confirmationKey(tool): params},
		RequestState:  confirmationKey(tool),
	}
}

// lookupConfirmation finds the answer to this tool's confirmation request.
//
// The lookup is by exact key, so an answer supplied under any other key — including
// one for a different tool — is not found, and the tool is asked again rather than
// silently allowed.
func lookupConfirmation(req *mcp.CallToolRequest, tool string) (*mcp.ElicitResult, bool) {
	if req.Params == nil || req.Params.InputResponses == nil {
		return nil, false
	}
	response, present := req.Params.InputResponses[confirmationKey(tool)]
	if !present {
		return nil, false
	}
	result, ok := response.(*mcp.ElicitResult)
	if !ok {
		// The client answered with something other than an elicitation result,
		// which is not an answer to the question that was asked.
		return nil, false
	}
	return result, true
}

// clientDeclaresElicitation reports whether the client said it can prompt its user.
//
// This is decided from the declared capability rather than by asking and reading the
// failure: an SDK error for an undeclared capability is not reliably distinguishable
// from a transport fault, and the two deserve different reasons.
func clientDeclaresElicitation(req *mcp.CallToolRequest) bool {
	if req.Session == nil {
		return false
	}
	capabilities := req.ClientCapabilities()
	return capabilities != nil && capabilities.Elicitation != nil
}

// classifyElicitError maps a transport failure onto a policy reason without
// propagating its text, which may carry a header or a body.
func classifyElicitError(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(ctx.Err(), context.DeadlineExceeded):
		return policy.ErrConfirmationTimedOut
	case errors.Is(err, context.Canceled):
		return context.Canceled
	default:
		return policy.ErrConfirmationUnavailable
	}
}

// interpretElicitResult treats anything short of an explicit acceptance as a
// refusal. A "cancel" action means the user dismissed the prompt without choosing,
// and a dismissal is not consent.
func interpretElicitResult(result *mcp.ElicitResult) error {
	if result == nil || result.Action != "accept" {
		return policy.ErrConfirmationDeclined
	}
	// An accepted form that leaves the box unticked is still a refusal. A client
	// that omits the property has accepted the prompt itself, which is consent to
	// the operation the prompt described.
	if value, present := result.Content[confirmationProperty]; present {
		if confirmed, ok := value.(bool); ok && !confirmed {
			return policy.ErrConfirmationDeclined
		}
	}
	return nil
}

// refuseConfirmation routes a reason through policy.RequireConfirmation, so the
// refusal wraps both ErrConfirmationRequired and the specific reason and its message
// names the cause. Keeping one place that authors these refusals means the wording a
// caller sees cannot drift from the sentinel a caller matches on.
func refuseConfirmation(reason error) error {
	return policy.RequireConfirmation(context.Background(), fixedConfirmer{reason},
		policy.ConfirmationRequest{Tier: policy.TierDestructive})
}

// fixedConfirmer answers with a predetermined reason.
type fixedConfirmer struct{ reason error }

func (f fixedConfirmer) Confirm(context.Context, policy.ConfirmationRequest) error {
	return f.reason
}

// confirmationSummary describes the operation in words the user can judge.
func confirmationSummary(registry *Registry, tool string) string {
	spec, ok := registry.Spec(tool)
	if !ok {
		return "an operation this server does not recognize"
	}
	return spec.Description + " (" + spec.Category + ")"
}

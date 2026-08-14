package mcpserver

import (
	"context"
	"errors"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/identity"
	"github.com/tamcore/garmin-mcp/internal/mcplog"
	"github.com/tamcore/garmin-mcp/internal/policy"
	"github.com/tamcore/garmin-mcp/internal/ratelimit"
)

// methodCallTool is the JSON-RPC method every gate here cares about.
const methodCallTool = "tools/call"

// callRecord is the mutable per-call scratchpad the logging middleware installs
// and the gates below it write into.
//
// A context value flows downward only, so an inner middleware cannot hand a value
// back to an outer one by deriving a new context. Installing a pointer that the
// inner gates mutate is the standard way across that grain, and it is why logging
// sits outside the gates it reports on.
type callRecord struct {
	outcome mcplog.Outcome
	reason  string
	status  mcplog.Status
}

type callRecordKey struct{}

func recordFromContext(ctx context.Context) *callRecord {
	record, _ := ctx.Value(callRecordKey{}).(*callRecord)
	return record
}

// installMiddleware wires the single receiving-middleware chain.
//
// The SDK applies AddReceivingMiddleware left to right, so the first entry is the
// outermost. One call is used rather than several, because a later call wraps
// *outside* an earlier one and the resulting order would be the reverse of what it
// reads like.
//
// The order is:
//
//  1. principal — everything below it keys off the principal, so it must be first.
//     It is also the only place a principal is ever set, which is what makes the
//     "a tool argument cannot select the principal" rule structural.
//  2. logging — outside every gate, so a refusal is reported with its latency and
//     its reason rather than vanishing. It installs the callRecord the gates write.
//  3. rate limiting — before the policy gate on purpose. A caller probing tools it
//     is not allowed to use must still be throttled; if policy ran first, a
//     scanning attacker would cost nothing to refuse and could probe without limit.
//  4. policy — the tier and scope intersection, plus destructive confirmation.
//     Last, so it is the final word before the handler runs.
func (s *Server) installMiddleware() {
	s.mcpServer.AddReceivingMiddleware(
		s.principalMiddleware(),
		s.loggingMiddleware(),
		ratelimit.Middleware(s.deps.Limiter, s.classifyTool, rateLimitObserver{}),
		s.policyMiddleware(),
	)
}

// principalMiddleware resolves the principal once and puts it on the context.
//
// A resolution failure refuses the tool call. There is no anonymous fallback: a
// tool that reached Garmin without a principal would be reaching it as somebody.
func (s *Server) principalMiddleware() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			principal, err := s.deps.Principals.Resolve(ctx)
			if err != nil {
				if method != methodCallTool {
					return next(ctx, method, req)
				}
				return errorResult("This request could not be attributed to an account, " +
					"so it was refused."), nil
			}
			return next(identity.WithPrincipal(ctx, principal), method, req)
		}
	}
}

// loggingMiddleware times the call and emits exactly one record for it.
//
// Only tools/call is recorded. Listing tools and pinging are protocol chatter that
// would bury the signal, and neither touches the user's Garmin account.
func (s *Server) loggingMiddleware() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != methodCallTool {
				return next(ctx, method, req)
			}

			record := &callRecord{}
			started := s.clock()
			result, err := next(context.WithValue(ctx, callRecordKey{}, record), method, req)

			s.deps.Logger.ToolCall(s.toolEvent(ctx, req, record, result, err, s.clock().Sub(started)))
			return result, err
		}
	}
}

// toolEvent assembles the log record. Every field is coarse: the exact tool name is
// carried only so mcplog can drop it unless the debug policy is on.
func (s *Server) toolEvent(ctx context.Context, req mcp.Request, record *callRecord,
	result mcp.Result, err error, latency time.Duration,
) mcplog.ToolEvent {
	tool := toolNameOf(req)
	event := mcplog.ToolEvent{
		RequestID:   s.nextRequestID(),
		PrincipalID: principalIDOf(ctx),
		ClientID:    clientIDOf(req),
		ToolName:    tool,
		Latency:     latency,
		Outcome:     record.outcome,
		Reason:      record.reason,
		Status:      record.status,
	}
	if spec, ok := s.registry.Spec(tool); ok {
		event.Category = spec.Category
		event.Tier = spec.Tier.String()
	}
	if event.Outcome != "" {
		return event
	}

	// No gate claimed the call, so the outcome is whatever the handler produced.
	switch {
	case err != nil:
		event.Outcome, event.Status = mcplog.OutcomeError, mcplog.StatusServerError
	case isErrorResult(result):
		event.Outcome, event.Status = mcplog.OutcomeError, mcplog.StatusUpstreamError
	default:
		event.Outcome, event.Status = mcplog.OutcomeOK, mcplog.StatusSuccess
	}
	return event
}

// policyMiddleware applies the tier and scope gate, then destructive confirmation.
func (s *Server) policyMiddleware() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != methodCallTool {
				return next(ctx, method, req)
			}

			decision := s.deps.Policy.Decide(ctx, toolNameOf(req))
			if !decision.Allowed {
				return s.deny(ctx, decision.Reason,
					"This tool was refused: "+decision.Reason+"."), nil
			}
			if !decision.RequiresConfirmation {
				return next(ctx, method, req)
			}

			outcome, pending, err := s.confirm(ctx, req, decision)
			switch outcome {
			case confirmationGranted:
				return next(ctx, method, req)
			case confirmationPending:
				return s.awaitConfirmation(ctx, pending), nil
			default:
				return s.deny(ctx, confirmationReason(err),
					"This tool was refused because it needs confirmation: "+err.Error()+"."), nil
			}
		}
	}
}

// awaitConfirmation records that the call is waiting on the user and returns the
// input-required result unchanged.
//
// This is neither a success nor a refusal: the tool has not run and nothing was
// denied. It is recorded as an ordinary outcome with a reason, so an operator can see
// a confirmation was requested without it looking like a policy violation.
func (s *Server) awaitConfirmation(ctx context.Context, pending *mcp.CallToolResult) *mcp.CallToolResult {
	if record := recordFromContext(ctx); record != nil {
		record.outcome = mcplog.OutcomeOK
		record.reason = "awaiting user confirmation of a destructive operation"
	}
	return pending
}

// deny records the refusal on the call record and builds the caller-facing result.
func (s *Server) deny(ctx context.Context, reason, text string) *mcp.CallToolResult {
	if record := recordFromContext(ctx); record != nil {
		record.outcome = mcplog.OutcomeDenied
		record.reason = reason
		record.status = mcplog.StatusClientError
	}
	return errorResult(text)
}

// confirm obtains destructive confirmation under a bounded deadline.
//
// An injected Deps.Confirmer takes precedence and is asked synchronously, which is
// the seam a deployment uses when it confirms somewhere other than the MCP client.
// Otherwise the client is asked, which may need a round trip; see confirmDestructive.
func (s *Server) confirm(ctx context.Context, req mcp.Request, decision policy.Decision) (
	confirmationOutcome, *mcp.CallToolResult, error,
) {
	timeout := s.deps.ConfirmationTimeout
	if timeout <= 0 {
		timeout = DefaultConfirmationTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if s.deps.Confirmer != nil {
		err := policy.RequireConfirmation(ctx, s.deps.Confirmer, policy.ConfirmationRequest{
			Tool:    decision.Tool,
			Tier:    decision.Tier,
			Summary: confirmationSummary(s.registry, decision.Tool),
		})
		if err != nil {
			return confirmationRefused, nil, err
		}
		return confirmationGranted, nil, nil
	}

	return s.confirmDestructive(ctx, req, decision)
}

// classifyTool maps a tool onto a rate-limit budget using the policy tier table.
//
// An unknown tool charges the read budget rather than escaping the limiter, so a
// caller cannot buy an unlimited allowance by inventing names.
func (s *Server) classifyTool(tool string) ratelimit.Kind {
	tier, ok := s.deps.Policy.TierOf(tool)
	if !ok || tier == policy.TierReadOnly {
		return ratelimit.KindRead
	}
	return ratelimit.KindWrite
}

// rateLimitObserver records a limiter refusal on the call record so the logging
// middleware, which wraps the limiter, reports the right outcome.
type rateLimitObserver struct{}

func (rateLimitObserver) RateLimited(ctx context.Context, result ratelimit.Result) {
	record := recordFromContext(ctx)
	if record == nil {
		return
	}
	record.outcome = mcplog.OutcomeRateLimited
	record.reason = result.Reason
	record.status = mcplog.StatusClientError
}

// confirmationReason renders the coarse log reason for a confirmation failure. It
// names the cause and never the underlying transport text.
func confirmationReason(err error) string {
	switch {
	case errors.Is(err, policy.ErrConfirmationUnsupported):
		return "confirmation is unsupported by the client"
	case errors.Is(err, policy.ErrConfirmationDeclined):
		return "confirmation was declined"
	case errors.Is(err, policy.ErrConfirmationTimedOut):
		return "confirmation timed out"
	default:
		return "confirmation could not be obtained"
	}
}

// errorResult builds a tool-level error result.
//
// A refused call is never a JSON-RPC transport error: a transport error is invisible
// to the model, whereas an error result reaches it as text it can read and act on.
func errorResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}
}

func isErrorResult(result mcp.Result) bool {
	callResult, ok := result.(*mcp.CallToolResult)
	return ok && callResult.IsError
}

func toolNameOf(req mcp.Request) string {
	params, ok := req.GetParams().(*mcp.CallToolParamsRaw)
	if !ok {
		return ""
	}
	return params.Name
}

func principalIDOf(ctx context.Context) string {
	principal, err := identity.FromContext(ctx)
	if err != nil {
		return ""
	}
	return principal.ID()
}

// clientIDOf names the calling client.
//
// Under stdio this is the client's self-reported implementation name, which is not
// an authenticated identity and is only ever used as a log label. The M2 remote path
// will take the client id from the verified token context instead.
func clientIDOf(req mcp.Request) string {
	callReq, ok := req.(*mcp.CallToolRequest)
	if !ok {
		return ""
	}
	info := callReq.ClientInfo()
	if info == nil {
		return ""
	}
	return info.Name
}

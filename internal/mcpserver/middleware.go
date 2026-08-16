package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
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
		argumentsMiddleware(),
		s.principalMiddleware(),
		s.loggingMiddleware(),
		ratelimit.Middleware(s.deps.Limiter, s.classifyTool, rateLimitObserver{}),
		s.policyMiddleware(),
	)
}

// argumentsMiddleware makes a tool call's arguments safe to validate.
//
// The SDK allocates an empty argument map and then unmarshals the request's raw
// arguments over it. A request carrying literal JSON null therefore replaces that
// allocated map with a nil map, and the value is still typed map[string]any, so
// the SDK proceeds to apply the schema's defaults and jsonschema-go v0.4.3 panics
// writing into a map it does not own (validate.go, applyDefaults). A caller can
// crash the server with one field.
//
// Absent arguments are safe by contrast: the SDK keeps its allocated empty map.
// This middleware therefore refuses null with an ordinary invalid-arguments
// result rather than rewriting it to {}, because the published schema says the
// arguments are an object and null is not one. Rewriting it would accept a
// malformed request and silently apply defaults the caller never asked for.
//
// It runs first, because the panic happens during argument validation, which is
// downstream of every other middleware. Remove it once the dependency stops
// writing into a nil map; the upstream report is linked in
// docs/implementation-status.md.
func argumentsMiddleware() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != methodCallTool {
				return next(ctx, method, req)
			}

			call, ok := req.(*mcp.CallToolRequest)
			if !ok || call == nil || call.Params == nil {
				return next(ctx, method, req)
			}
			if isJSONNull(call.Params.Arguments) {
				return errorResult("The arguments field was null. Send an object, " +
					"or omit the field entirely."), nil
			}

			return next(ctx, method, req)
		}
	}
}

// isJSONNull reports whether raw is the JSON null literal, ignoring surrounding
// whitespace, which is what a typed nil map marshals to.
func isJSONNull(raw json.RawMessage) bool {
	return string(bytes.TrimSpace(raw)) == "null"
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
				return s.dispatch(ctx, method, req, decision, next)
			}

			outcome, pending, err := s.confirm(ctx, req, decision)
			switch outcome {
			case confirmationGranted:
				return s.dispatch(ctx, method, req, decision, next)
			case confirmationPending:
				return s.awaitConfirmation(ctx, pending), nil
			default:
				return s.deny(ctx, confirmationReason(err),
					"This tool was refused because it needs confirmation: "+err.Error()+"."), nil
			}
		}
	}
}

// dispatch applies the operator's safety delay, then runs the tool.
//
// It is reached only after every gate has allowed the call, which is the whole
// design of the pause: a refused call must not wait, both because waiting to say no
// costs the server the wait and because a prober would learn the gate's timing from
// it.
//
// A read never waits. Reads change nothing, so there is nothing to reconsider during
// the pause and the delay would be latency with no safety in it.
func (s *Server) dispatch(
	ctx context.Context, method string, req mcp.Request,
	decision policy.Decision, next mcp.MethodHandler,
) (mcp.Result, error) {
	if err := s.awaitSafetyDelay(ctx, decision); err != nil {
		return s.deny(ctx, "the safety delay was interrupted before the tool ran",
			"This tool did not run: the configured safety delay was interrupted, "+
				"so nothing was sent to Garmin."), nil
	}
	return next(ctx, method, req)
}

// awaitSafetyDelay waits out the configured pause for a write or destructive call.
//
// The wait is interruptible, and that is the point rather than a detail: a pause
// nothing can interrupt is latency, not safety. When the caller cancels during it,
// this returns the cancellation and the tool never runs.
func (s *Server) awaitSafetyDelay(ctx context.Context, decision policy.Decision) error {
	if s.deps.SafetyDelay <= 0 || decision.Tier == policy.TierReadOnly {
		return nil
	}
	// The pause is not logged as its own field: the call's recorded latency already
	// contains it, and mcplog's field set is an allowlist worth keeping small.
	return s.sleep(ctx, s.deps.SafetyDelay)
}

// sleep waits for d, or until the context ends. It is the injected seam in tests.
func (s *Server) sleep(ctx context.Context, d time.Duration) error {
	if s.deps.Sleep != nil {
		return s.deps.Sleep(ctx, d)
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	// A select whose cases are both ready picks one at random, so the timer can win
	// a race a cancellation had already entered. Without this recheck a caller that
	// cancelled a whisker before the pause elapsed would still have its write sent.
	return ctx.Err()
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

package ratelimit

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/identity"
)

// methodCallTool is the only method this middleware gates. Listing tools, reading
// a resource, or pinging costs the Garmin account nothing, so charging a budget
// for them would only make discovery unreliable.
const methodCallTool = "tools/call"

// A Classifier maps a tool name onto the budget its call is charged to.
//
// It returns a Kind unconditionally rather than an optional one: an unrecognized
// tool must still be charged, or a caller could evade the limiter by inventing
// names. The server supplies a classifier backed by the policy tier table.
type Classifier func(tool string) Kind

// An Observer is told about every limiter decision the middleware makes.
//
// It exists so the logging middleware, which sits outside this one, can record a
// rate-limited outcome without this package importing it. Implementations must not
// block: they run on the request path.
type Observer interface {
	RateLimited(ctx context.Context, result Result)
}

// Middleware returns MCP receiving middleware that charges each tools/call to the
// calling principal's budget.
//
// A limited call returns a tool-level error result, not a transport error. That is
// the required behavior: a JSON-RPC error is invisible to the model, whereas an
// error result with IsError set reaches the model as text it can read and retry on.
//
// A nil limiter makes the middleware a transparent pass-through. A nil classifier
// falls back to the read budget rather than skipping the limiter, so a wiring
// mistake degrades to mis-charging rather than to no limit at all. A nil observer
// is ignored.
func Middleware(limiter *Limiter, classify Classifier, observer Observer) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if limiter == nil || method != methodCallTool {
				return next(ctx, method, req)
			}

			result := limiter.Allow(principalOf(ctx), kindOf(classify, toolNameOf(req)))
			if result.Allowed {
				return next(ctx, method, req)
			}

			// The observer hears about refusals only. An allowed call is already
			// recorded by the logging middleware that wraps this one, so
			// reporting it here would double-count it.
			if observer != nil {
				observer.RateLimited(ctx, result)
			}
			return ErrorResult(result), nil
		}
	}
}

// principalOf returns the resolved principal, or the zero Principal when the
// request carries none. Allow refuses the zero value, so an unresolved request is
// refused rather than pooled into a shared budget.
func principalOf(ctx context.Context) identity.Principal {
	principal, err := identity.FromContext(ctx)
	if err != nil {
		return identity.Principal{}
	}
	return principal
}

// toolNameOf extracts the tool name from a tools/call request, or "" if the params
// are not the expected shape.
func toolNameOf(req mcp.Request) string {
	params, ok := req.GetParams().(*mcp.CallToolParamsRaw)
	if !ok {
		return ""
	}
	return params.Name
}

func kindOf(classify Classifier, tool string) Kind {
	if classify == nil {
		return KindRead
	}
	return classify(tool)
}

// retryRounding keeps the advertised retry delay readable without understating it
// by more than a tenth of a second.
const retryRounding = 100 * 1e6

// ErrorResult builds the caller-actionable MCP error result for a limited call.
//
// The text names the budget and the retry delay and nothing else: no principal
// identifier, no tool name, no upstream detail.
func ErrorResult(result Result) *mcp.CallToolResult {
	text := result.Reason
	if text == "" {
		text = "the " + result.Kind.String() + " rate-limit budget for this account is exhausted"
	}
	text = "Rate limit reached. " + text + "."
	if result.RetryAfter > 0 {
		text += " Retry after " + result.RetryAfter.Round(retryRounding).String() + "."
	}

	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}
}

package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const resultTypeField = "resultType"

// resultTypeMiddleware stamps resultType on tools/call results at 2026-07-28 and later.
//
// The SDK sets it only on a result a tool handler returns. A result this server's
// own middleware builds — a refusal, a rate-limit answer, a confirmation request —
// never reaches that code, and a client on the new protocol rejects it as malformed.
func resultTypeMiddleware() mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			result, err := next(ctx, method, req)
			callReq, isCall := req.(*mcp.CallToolRequest)
			callResult, isCallResult := result.(*mcp.CallToolResult)
			if err != nil || !isCall || !isCallResult || callResult == nil ||
				callReq.ProtocolVersion() < protocolVersionMultiRoundTrip {
				return result, err
			}
			return typedResult{callResult}, nil
		}
	}
}

// typedResult is a tools/call result whose encoding always names its resultType.
type typedResult struct{ *mcp.CallToolResult }

func (r typedResult) MarshalJSON() ([]byte, error) {
	raw, err := json.Marshal(r.CallToolResult)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("re-encoding tool result: %w", err)
	}
	if _, present := fields[resultTypeField]; present {
		return raw, nil
	}
	resultType := `"complete"`
	if r.InputRequests != nil {
		resultType = `"input_required"`
		delete(fields, "content")
	}
	fields[resultTypeField] = json.RawMessage(resultType)
	return json.Marshal(fields)
}

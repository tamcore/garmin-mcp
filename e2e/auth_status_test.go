//go:build e2e

package e2e

import (
	"encoding/json"
	"testing"
)

func TestGarminAuthStatusReportsIsolatedMissingCredentials(t *testing.T) {
	bin := buildBinary(t)
	cmd, stdin, scanner, stderr := startStdioSession(t, bin)

	writeFrame(t, stdin,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2026-07-28","capabilities":{},"clientInfo":{"name":"auth-status-e2e","version":"1"}}}`)
	_ = readFrame(t, scanner)
	writeFrame(t, stdin, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	writeFrame(t, stdin,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"garmin_auth_status","arguments":{}}}`)
	responseFrame := readFrame(t, scanner)
	if err := stdin.Close(); err != nil {
		t.Errorf("close stdin: %v", err)
	}
	waitStdioSession(t, cmd, stderr)

	var response struct {
		ID     int             `json:"id"`
		Error  json.RawMessage `json:"error"`
		Result struct {
			IsError           bool `json:"isError"`
			StructuredContent struct {
				Authenticated bool            `json:"authenticated"`
				Account       json.RawMessage `json:"account"`
				Reason        string          `json:"reason"`
			} `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(responseFrame), &response); err != nil {
		t.Fatalf("decode tools/call response %q: %v", responseFrame, err)
	}
	if response.ID != 2 || len(response.Error) != 0 || response.Result.IsError {
		t.Fatalf("garmin_auth_status failed: id %d, JSON-RPC error %s, isError %t",
			response.ID, response.Error, response.Result.IsError)
	}
	status := response.Result.StructuredContent
	if status.Authenticated || len(status.Account) != 0 || status.Reason != "no_credentials" {
		t.Fatalf("garmin_auth_status = %+v, want unauthenticated no_credentials without account", status)
	}
}

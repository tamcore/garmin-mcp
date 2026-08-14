package tools_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/tamcore/garmin-mcp/internal/tools"
)

// TestPublishedSchemasCarryTheDeclaredBounds asserts what a client actually
// receives from tools/list.
//
// The schema the SDK infers from a handler's Go input type carries types and
// descriptions and nothing else, so a bound such as "limit is 1 to 100, default
// 20" would be enforced in the handler and invisible to the caller. A client
// builds its call from the published schema, so a thin contract produces
// avoidable invalid calls. This test fails if registration stops publishing the
// declared schema.
func TestPublishedSchemasCarryTheDeclaredBounds(t *testing.T) {
	t.Parallel()

	h := newHarness(t, readScript())

	published := map[string]map[string]any{}
	for _, tool := range listedTools(t, h) {
		encoded, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatalf("marshal published schema for %q: %v", tool.Name, err)
		}
		var schema map[string]any
		if err := json.Unmarshal(encoded, &schema); err != nil {
			t.Fatalf("decode published schema for %q: %v", tool.Name, err)
		}
		published[tool.Name] = schema
	}

	// These keywords are exactly the ones inference cannot produce, so each is
	// evidence that the declaration reached the wire.
	keywords := []string{"minimum", "maximum", "maxLength", "pattern", "format", "default", "anyOf"}

	compared := 0
	for name, contract := range tools.Contracts() {
		declaredProps, _ := contract.Schema.JSON()["properties"].(map[string]any)
		if len(declaredProps) == 0 {
			continue // a tool with no arguments has nothing to publish
		}

		wire, ok := published[name]
		if !ok {
			t.Errorf("%s: registered contract is absent from tools/list", name)
			continue
		}
		wireProps, _ := wire["properties"].(map[string]any)

		for property, raw := range declaredProps {
			declaredProp, _ := raw.(map[string]any)
			wireProp, found := wireProps[property].(map[string]any)
			if !found {
				t.Errorf("%s.%s: declared but not published", name, property)
				continue
			}

			for _, keyword := range keywords {
				want, declares := declaredProp[keyword]
				if !declares {
					continue
				}
				got, publishes := wireProp[keyword]
				if !publishes {
					t.Errorf("%s.%s: %q declared as %v but absent from the published schema",
						name, property, keyword, want)
					continue
				}
				if !jsonEqual(got, want) {
					t.Errorf("%s.%s: %q published as %v, declared as %v",
						name, property, keyword, got, want)
				}
				compared++
			}
		}
	}

	if compared == 0 {
		t.Fatal("no constraint keyword was compared, so this test would pass vacuously")
	}
	t.Logf("compared %d published constraint keywords", compared)
}

// TestNullArgumentsAreRefusedNotFatal pins the guard for a crash that one field in
// one request could cause.
//
// The SDK allocates an empty argument map and unmarshals the raw arguments over
// it, so a literal JSON null replaces that map with a nil one while the value
// stays typed map[string]any. The SDK then applies the schema's defaults and
// jsonschema-go v0.4.3 panics writing into a map it does not own. A refusal is the
// correct answer: the published schema says the arguments are an object, and null
// is not one.
func TestNullArgumentsAreRefusedNotFatal(t *testing.T) {
	t.Parallel()

	h := newHarness(t, readScript())

	// A typed nil map is what a careless client sends; it marshals to
	// "arguments":null.
	var nilArgs map[string]any
	result, err := h.session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      tools.ToolGetDevices,
		Arguments: nilArgs,
	})
	if err != nil {
		t.Fatalf("CallTool transport error = %v; the server must answer, not drop the connection", err)
	}
	if !result.IsError {
		t.Fatal("null arguments produced a success result")
	}
	if text := resultText(result); !strings.Contains(strings.ToLower(text), "null") {
		t.Errorf("refusal %q does not tell the caller what was wrong", text)
	}

	// A refusal must not be a broken server: the next call still works.
	next, err := h.session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      tools.ToolGetDevices,
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("the session did not survive the refusal: %v", err)
	}
	if next.IsError {
		t.Errorf("the call after the refusal failed: %s", resultText(next))
	}
}

// TestOmittedArgumentsAreAccepted covers the neighbouring case the SDK does
// handle, so the guard above cannot quietly widen into refusing valid calls.
func TestOmittedArgumentsAreAccepted(t *testing.T) {
	t.Parallel()

	h := newHarness(t, readScript())

	result, err := h.session.CallTool(t.Context(), &mcp.CallToolParams{Name: tools.ToolGetDevices})
	if err != nil {
		t.Fatalf("CallTool transport error = %v", err)
	}
	if result.IsError {
		t.Fatalf("omitted arguments were refused: %s", resultText(result))
	}
}

func jsonEqual(a, b any) bool {
	left, err := json.Marshal(a)
	if err != nil {
		return false
	}
	right, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return string(left) == string(right)
}

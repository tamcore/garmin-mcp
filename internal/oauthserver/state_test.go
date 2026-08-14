package oauthserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

// strippedState is the method-stripping alias attack against client state.
type strippedState ClientState

const testState = "opaque-client-state-ü-%20-&=?#-value"

func mustClientState(t *testing.T) ClientState {
	t.Helper()
	state, err := ParseClientState(testState)
	if err != nil {
		t.Fatalf("ParseClientState: %v", err)
	}
	return state
}

func TestParseClientStatePreservesBytesExactly(t *testing.T) {
	state := mustClientState(t)

	if got := state.Reveal(); got != testState {
		t.Fatalf("Reveal() = %q, want %q", got, testState)
	}
	if state.IsZero() {
		t.Fatal("a non-empty state must not report IsZero")
	}
}

func TestParseClientStateAcceptsAbsentState(t *testing.T) {
	state, err := ParseClientState("")
	if err != nil {
		t.Fatalf("an absent state is not an error: %v", err)
	}
	if !state.IsZero() || state.Reveal() != "" {
		t.Fatal("the empty state must be the zero ClientState")
	}
}

func TestParseClientStateRejectsUnboundedOrUnsafeInput(t *testing.T) {
	for name, raw := range map[string]string{
		"over-long state":          strings.Repeat("a", MaxClientStateLen+1),
		"newline inside the state": "state\nwith-newline",
		"nul":                      "state\x00truncated",
		"cr":                       "state\rwith-cr",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseClientState(raw); !errors.Is(err, ErrInvalidState) {
				t.Fatalf("ParseClientState error = %v, want ErrInvalidState", err)
			}
		})
	}
}

func TestClientStateRenderingsNeverRevealTheState(t *testing.T) {
	state := mustClientState(t)

	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("json.Marshal(ClientState): %v", err)
	}
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("authorizing", slog.Any("state", state))

	renderings := map[string]string{
		labelString:    state.String(),
		labelGoString:  state.GoString(),
		labelFmtV:      fmt.Sprintf("%v", state),
		labelFmtS:      fmt.Sprintf("as-%s", state),
		labelFmtPlusV:  fmt.Sprintf("%+v", state),
		labelFmtSharpV: fmt.Sprintf("%#v", state),
		"fmt pointer":  fmt.Sprintf("%v", &state),
		"MarshalJSON":  string(encoded),
		"slog":         buf.String(),
	}
	for label, rendered := range renderings {
		if strings.Contains(rendered, testState) {
			t.Fatalf("%s leaked the client state: %q", label, rendered)
		}
		if rendered == "" {
			t.Fatalf("%s rendered as the empty string", label)
		}
	}
}

func TestClientStateAliasCannotBypassRedaction(t *testing.T) {
	state := mustClientState(t)
	stripped := strippedState(state)

	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%d", "%x"} {
		for _, rendered := range []string{
			fmt.Sprintf(verb, stripped),
			fmt.Sprintf(verb, &stripped),
		} {
			if strings.Contains(rendered, testState) {
				t.Fatalf("method-stripped alias under %s leaked the state: %q", verb, rendered)
			}
		}
	}
}

func TestZeroClientStateRendersWithoutPanicking(t *testing.T) {
	var state ClientState

	if state.String() == "" || state.GoString() == "" {
		t.Fatal("the zero ClientState rendered as the empty string")
	}
	if _, err := json.Marshal(state); err != nil {
		t.Fatalf("json.Marshal(zero ClientState): %v", err)
	}
}

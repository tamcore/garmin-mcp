package config

import (
	"errors"
	"strings"
	"testing"

	"github.com/tamcore/garmin-mcp/internal/garmin/protocol"
)

// Synthetic values shared by the configuration tests. They are constants so a
// typo in one case cannot silently diverge from another.
const (
	toolActivities = "get_activities"
	toolSleep      = "get_sleep_data"
	toolDelete     = "delete_workout"
	cidrPrivate    = "10.0.0.0/8"
	mutatedValue   = "mutated"
	publicHTTPS    = "https://mcp.example.test"
	tlsCertPath    = "/etc/garmin-mcp/tls.crt"
	tlsKeyPath     = "/etc/garmin-mcp/tls.key"
	tokenFilePath  = "/home/user/.garmin-mcp/garmin_tokens.json"
	masterKeyPath  = "/var/lib/garmin-mcp/master.key"
	databasePath   = "/var/lib/garmin-mcp/state.db"
	levelDebug     = "debug"
	levelWarn      = "warn"
	levelError     = "error"
	formatJSON     = "json"
	envLogLevel    = "GARMIN_MCP_LOG_LEVEL"
)

func TestDefaultIsValidAndSafe(t *testing.T) {
	t.Parallel()

	cfg := Default()

	if cfg.Transport != TransportStdio {
		t.Errorf("Transport = %q, want %q", cfg.Transport, TransportStdio)
	}
	if cfg.EnableWriteTools {
		t.Error("EnableWriteTools = true, want false: defaults must be read-only")
	}
	if cfg.EnableDestructiveTools {
		t.Error("EnableDestructiveTools = true, want false: defaults must be read-only")
	}
	if cfg.AllowInsecureHTTP {
		t.Error("AllowInsecureHTTP = true, want false: defaults must fail closed")
	}
	if got := cfg.Region.Domain(); got != protocol.DomainGlobal {
		t.Errorf("Region = %q, want %q", got, protocol.DomainGlobal)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Default().Validate() = %v, want nil", err)
	}
}

func TestDefaultReturnsIndependentValues(t *testing.T) {
	t.Parallel()

	first := Default()
	first.ToolAllowlist = append(first.ToolAllowlist, toolActivities)

	if len(Default().ToolAllowlist) != 0 {
		t.Error("Default() shares slice state with a previously returned value")
	}
}

func TestCloneDoesNotShareSliceState(t *testing.T) {
	t.Parallel()

	original := Default()
	original.ToolAllowlist = []string{toolActivities}
	original.ToolDenylist = []string{toolDelete}
	original.TrustedProxyCIDRs = []string{cidrPrivate}

	clone := original.Clone()
	clone.ToolAllowlist[0] = mutatedValue
	clone.ToolDenylist[0] = mutatedValue
	clone.TrustedProxyCIDRs[0] = mutatedValue

	if original.ToolAllowlist[0] != toolActivities {
		t.Error("Clone shares ToolAllowlist backing array")
	}
	if original.ToolDenylist[0] != toolDelete {
		t.Error("Clone shares ToolDenylist backing array")
	}
	if original.TrustedProxyCIDRs[0] != cidrPrivate {
		t.Error("Clone shares TrustedProxyCIDRs backing array")
	}
}

func TestParseTransport(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    Transport
		wantErr bool
	}{
		{name: "stdio", input: "stdio", want: TransportStdio},
		{name: "streamable http", input: "streamable-http", want: TransportStreamableHTTP},
		{name: "surrounding space", input: "  stdio\t", want: TransportStdio},
		{name: "upper case", input: "STDIO", want: TransportStdio},
		{name: "empty selects stdio", input: "", want: TransportStdio},
		{name: "sse is not supported", input: "sse", wantErr: true},
		{name: "http is not an alias", input: "http", wantErr: true},
		{name: "unknown", input: "telepathy", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseTransport(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseTransport(%q) = %q, want error", tc.input, got)
				}
				if !errors.Is(err, ErrUnsupportedTransport) {
					t.Errorf("error %v does not match ErrUnsupportedTransport", err)
				}
				if !errors.Is(err, ErrInvalidConfig) {
					t.Errorf("error %v does not match ErrInvalidConfig", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseTransport(%q) error = %v", tc.input, err)
			}
			if got != tc.want {
				t.Errorf("ParseTransport(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestTransportErrorNeverEchoesInput(t *testing.T) {
	t.Parallel()

	// A rejected transport is caller-supplied text. Echoing it would carry
	// attacker-controlled content into an operator log line.
	const hostile = "stdio\n\rINJECTED"

	_, err := ParseTransport(hostile)
	if err == nil {
		t.Fatal("ParseTransport accepted a hostile transport value")
	}
	if strings.Contains(err.Error(), "INJECTED") {
		t.Errorf("error %q echoes the rejected input", err.Error())
	}
}

func TestTransportsListsEveryKnownTransport(t *testing.T) {
	t.Parallel()

	got := Transports()
	want := []Transport{TransportStdio, TransportStreamableHTTP}

	if len(got) != len(want) {
		t.Fatalf("Transports() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Transports() = %v, want %v", got, want)
		}
	}

	got[0] = mutatedValue
	if Transports()[0] != TransportStdio {
		t.Error("Transports() exposes shared package state")
	}
}

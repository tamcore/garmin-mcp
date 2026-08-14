package client

import (
	"encoding/json"
	"log/slog"
	"strconv"
)

// Presence labels the redacting renderers use, so a reader can tell "absent"
// from "withheld".
const (
	labelSet   = "set"
	labelUnset = "unset"
)

// presence renders whether material is present without revealing it.
func presence(present bool) string {
	if present {
		return labelSet
	}
	return labelUnset
}

// redactedDisplayName is the only shape a DisplayName ever renders as.
type redactedDisplayName struct {
	DisplayName string `json:"displayName"`
}

func (n DisplayName) redacted() redactedDisplayName {
	return redactedDisplayName{DisplayName: presence(!n.IsZero())}
}

// String reports whether a display name is present, never which one. The name is
// identity material, and an identity in a log line is exactly what a
// multi-tenant server must not emit.
func (n DisplayName) String() string {
	return "DisplayName{displayName:" + n.redacted().DisplayName + "}"
}

// GoString keeps %#v as redacted as %v.
func (n DisplayName) GoString() string { return n.String() }

// MarshalJSON encodes presence, so a display name cannot reach a JSON log sink
// or an error payload by being embedded in some larger structure.
func (n DisplayName) MarshalJSON() ([]byte, error) { return json.Marshal(n.redacted()) }

// LogValue keeps slog from reaching the material through reflection.
func (n DisplayName) LogValue() slog.Value {
	return slog.GroupValue(slog.String("displayName", n.redacted().DisplayName))
}

// quoteLabel quotes a short label so an empty value stays visible in a message.
func quoteLabel(value string) string { return strconv.Quote(value) }

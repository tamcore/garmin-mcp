package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// jsonNull is the JSON null literal, which every union decoder treats as absent.
const jsonNull = "null"

// DecodeJSON decodes a payload into out, tolerantly.
//
// Tolerance is deliberate and one-directional: an unknown field is ignored, so a
// Garmin schema change cannot fail an otherwise useful response, while a body that
// is not JSON at all is reported as ErrMalformedPayload. A normalized 204, and any
// empty body, leaves out untouched and returns nil.
//
// The failure carries the payload's sanitized labels and never the payload: the
// decoder error is wrapped as a cause, and *APIError renders an unrecognized cause
// as its Go type name, so no fragment of a health or location record reaches a log
// line through an error string.
func DecodeJSON(p Payload, out any) error {
	if p.NoContent() {
		return nil
	}

	decoder := json.NewDecoder(bytes.NewReader(p.Bytes()))
	if err := decoder.Decode(out); err != nil {
		return decodeFailure(p, err)
	}
	return nil
}

// decodeFailure builds the labeled *APIError for an undecodable payload.
func decodeFailure(p Payload, cause error) error {
	return &APIError{
		Op:       p.Op(),
		Endpoint: p.Endpoint(),
		Status:   p.Status(),
		Kind:     KindMalformedPayload,
		Err:      fmt.Errorf("%w: %w", ErrMalformedPayload, cause),
	}
}

// Number is a union decoder for a numeric field Garmin may send as a JSON number,
// as a numeric string, as null, or as an empty string.
//
// Upstream tolerates all four because the same field arrives differently per
// device and per locale; a strict float64 field turns one of them into a decode
// failure for the whole response.
//
// The zero value reports absent.
type Number struct {
	value   float64
	present bool
}

// NewNumber returns a present Number.
func NewNumber(value float64) Number { return Number{value: value, present: true} }

// UnmarshalJSON accepts a number, a numeric string, null and an empty string.
func (n *Number) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == jsonNull {
		*n = Number{}
		return nil
	}

	if quoted, ok := unquoteJSONString(trimmed); ok {
		inner := strings.TrimSpace(quoted)
		if inner == "" {
			*n = Number{}
			return nil
		}
		return n.parse(inner)
	}
	return n.parse(trimmed)
}

// parse stores value, or reports a payload this decoder cannot tolerate.
func (n *Number) parse(value string) error {
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fmt.Errorf("garmin api: numeric field is not a number: %w", ErrMalformedPayload)
	}
	*n = NewNumber(parsed)
	return nil
}

// MarshalJSON encodes the number, or null when absent. A measurement is not a
// secret on its own; the model that holds it is documented as sensitive.
func (n Number) MarshalJSON() ([]byte, error) {
	if !n.present {
		return []byte(jsonNull), nil
	}
	return json.Marshal(n.value)
}

// IsSet reports whether the field was present and numeric.
func (n Number) IsSet() bool { return n.present }

// Float64 returns the value and whether it was present.
func (n Number) Float64() (float64, bool) { return n.value, n.present }

// Int64 returns the truncated value and whether it was present.
func (n Number) Int64() (int64, bool) { return int64(n.value), n.present }

// Text is a union decoder for a field Garmin may send as a string, a number or a
// boolean. The zero value reports absent.
type Text struct {
	value   string
	present bool
}

// NewText returns a present Text.
func NewText(value string) Text { return Text{value: value, present: true} }

// UnmarshalJSON accepts a string, a number, a boolean and null.
func (t *Text) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == jsonNull {
		*t = Text{}
		return nil
	}
	if unquoted, ok := unquoteJSONString(trimmed); ok {
		*t = NewText(unquoted)
		return nil
	}
	switch trimmed {
	case "true", "false":
		*t = NewText(trimmed)
		return nil
	}
	if _, err := strconv.ParseFloat(trimmed, 64); err != nil {
		return fmt.Errorf("garmin api: text field is neither string, number nor boolean: %w", ErrMalformedPayload)
	}
	*t = NewText(trimmed)
	return nil
}

// MarshalJSON encodes the string, or null when absent.
func (t Text) MarshalJSON() ([]byte, error) {
	if !t.present {
		return []byte(jsonNull), nil
	}
	return json.Marshal(t.value)
}

// IsSet reports whether the field was present.
func (t Text) IsSet() bool { return t.present }

// Value returns the string form and whether it was present.
func (t Text) Value() (string, bool) { return t.value, t.present }

// List is a union decoder for a field Garmin sends sometimes as an array and
// sometimes as a single object, which is how several per-activity collections
// behave across activity types. null and an absent field decode to no items.
type List[T any] struct {
	items []T
}

// NewList returns a List over items.
func NewList[T any](items ...T) List[T] { return List[T]{items: items} }

// UnmarshalJSON accepts an array, a single object and null.
func (l *List[T]) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || string(trimmed) == jsonNull {
		*l = List[T]{}
		return nil
	}

	if trimmed[0] == '[' {
		var items []T
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return err
		}
		*l = List[T]{items: items}
		return nil
	}

	var single T
	if err := json.Unmarshal(trimmed, &single); err != nil {
		return err
	}
	*l = List[T]{items: []T{single}}
	return nil
}

// MarshalJSON always encodes an array, so the volatile shape is normalized for
// every consumer downstream.
func (l List[T]) MarshalJSON() ([]byte, error) {
	if l.items == nil {
		return []byte("[]"), nil
	}
	return json.Marshal(l.items)
}

// Items returns a copy of the decoded items, so no caller can mutate the value
// another caller holds.
func (l List[T]) Items() []T {
	out := make([]T, len(l.items))
	copy(out, l.items)
	return out
}

// Len is the item count.
func (l List[T]) Len() int { return len(l.items) }

// unquoteJSONString reports the unquoted content of a JSON string literal. The
// second result is false when data is not a string literal.
func unquoteJSONString(data string) (string, bool) {
	if len(data) < 2 || data[0] != '"' {
		return "", false
	}
	var unquoted string
	if err := json.Unmarshal([]byte(data), &unquoted); err != nil {
		return "", false
	}
	return unquoted, true
}

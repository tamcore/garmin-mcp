package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/tamcore/garmin-mcp/internal/garmin/client"
)

// The token-level half of the streaming decode in exercisecatalogparse.go. Every
// object this package reads is opened, walked member by member, and closed here,
// so a bound can be applied at the key that crosses it rather than after the whole
// document has been expanded.

// expectObject consumes one opening brace.
func expectObject(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return malformed("the document", err)
	}
	if delim, ok := token.(json.Delim); !ok || delim != '{' {
		return fmt.Errorf("%w: the exercise catalog is not the published document",
			client.ErrMalformedPayload)
	}
	return nil
}

// objectKey reads one member name.
func objectKey(decoder *json.Decoder) (string, error) {
	token, err := decoder.Token()
	if err != nil {
		return "", malformed("a member name", err)
	}
	key, ok := token.(string)
	if !ok {
		return "", fmt.Errorf("%w: the exercise catalog carries a member without a name",
			client.ErrMalformedPayload)
	}
	return key, nil
}

// skipValue discards one value this package does not read. It is bounded by the
// byte cap on the response, because a skipped value is never expanded.
func skipValue(decoder *json.Decoder) error {
	var ignored json.RawMessage
	if err := decoder.Decode(&ignored); err != nil {
		return malformed("an ignored member", err)
	}
	return nil
}

// endObject consumes one closing brace. Decoder.More does not consume it, and a
// walk that skipped it would read on at the wrong nesting depth.
func endObject(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return malformed("the end of an object", err)
	}
	if delim, ok := token.(json.Delim); !ok || delim != '}' {
		return fmt.Errorf("%w: the exercise catalog is not the published document",
			client.ErrMalformedPayload)
	}
	return nil
}

// openList consumes an opening bracket, or a null in its place, and reports
// whether a list follows.
func openList(decoder *json.Decoder) (bool, error) {
	token, err := decoder.Token()
	if err != nil {
		return false, malformed("a list", err)
	}
	if token == nil {
		return false, nil
	}
	if delim, ok := token.(json.Delim); !ok || delim != '[' {
		return false, fmt.Errorf("%w: the exercise catalog carries a muscle list that is "+
			"not a list", client.ErrMalformedPayload)
	}
	return true, nil
}

// endArray consumes one closing bracket.
func endArray(decoder *json.Decoder) error {
	return expectDelim(decoder, ']', "the end of a list")
}

// stringElement reads one string element of a list.
func stringElement(decoder *json.Decoder) (string, error) {
	token, err := decoder.Token()
	if err != nil {
		return "", malformed("a list element", err)
	}
	value, ok := token.(string)
	if !ok {
		return "", fmt.Errorf("%w: the exercise catalog carries a non-string list element",
			client.ErrMalformedPayload)
	}
	return value, nil
}

// expectEnd requires the stream to be finished, so a recognized catalog followed
// by a second value or by garbage is refused rather than served.
func expectEnd(decoder *json.Decoder) error {
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: the exercise catalog carries data after the document",
			client.ErrMalformedPayload)
	}
	return nil
}

// expectDelim consumes one expected delimiter.
func expectDelim(decoder *json.Decoder, want json.Delim, what string) error {
	token, err := decoder.Token()
	if err != nil {
		return malformed(what, err)
	}
	if delim, ok := token.(json.Delim); !ok || delim != want {
		return fmt.Errorf("%w: the exercise catalog is not the published document",
			client.ErrMalformedPayload)
	}
	return nil
}

// malformed wraps a decode failure as a payload error.
func malformed(what string, err error) error {
	return fmt.Errorf("%w: the exercise catalog could not be read at %s: %w",
		client.ErrMalformedPayload, what, err)
}

// boundCrossed reports a cardinality bound a document crossed.
func boundCrossed(what string, got, limit int) error {
	return fmt.Errorf("%w: the fetched exercise catalog carries %d %s, over the bound of %d",
		client.ErrMalformedPayload, got, what, limit)
}

// repeatedMember reports a structural member the document carries twice.
func repeatedMember(member string) error {
	return fmt.Errorf("%w: the fetched exercise catalog carries the %q member twice, and "+
		"which one is read would depend on the order it carries them in",
		client.ErrMalformedPayload, member)
}

// collision reports two raw keys that normalize to one.
func collision(what, key string) error {
	return fmt.Errorf("%w: the fetched exercise catalog carries two %s keys that normalize "+
		"to %q, and resolving them would depend on map order",
		client.ErrMalformedPayload, what, key)
}

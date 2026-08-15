package tools

import (
	"encoding/json"
	"strings"
)

// Bounds on the sanitising walk. A Garmin document is untrusted input: it can drift,
// and a drifted or hostile document must not be able to exhaust the stack or the heap
// of a server that is only trying to hand it on.
//
// maxSanitizeDepth bounds the recursion, so 64 KiB of opening brackets costs 24 frames
// rather than thirty thousand. maxSanitizeNodes bounds the total work, so a wide
// document costs a bounded number of map and slice allocations. Both are far above any
// document this project has sampled.
const (
	maxSanitizeDepth = 24
	maxSanitizeNodes = 20000
)

// identifyingKeys are dropped on an exact, case-insensitive match of the whole key.
//
// Each one is too short to match as a substring without deleting unrelated data:
// "lat" occurs inside "latency", "relation" and "translate", and "lon" inside "along"
// and "belong". Matching them whole is the deliberate trade — a coordinate named "lat"
// is caught, and a duration named "latency" survives.
var identifyingKeys = map[string]struct{}{
	"lat":      {},
	"lon":      {},
	"lng":      {},
	"long":     {},
	"userpk":   {},
	"ownerpk":  {},
	"email":    {},
	"username": {},
}

// identifyingFragments are dropped on a case-insensitive substring match anywhere in
// the key.
//
// Every fragment is long enough that an accidental hit on an unrelated Garmin field is
// implausible, which is what makes the substring rule safe here and unsafe for the
// short names above. "profilepk" catches userProfilePK and every other *ProfilePK,
// "latitude" and "longitude" catch startLatitude, endLongitude and the weather
// variants, and "displayname" catches ownerDisplayName as well as the bare key.
var identifyingFragments = []string{
	"profilepk",
	"profileid",
	"userid",
	"ownerid",
	"displayname",
	"latitude",
	"longitude",
	"position",
}

// sanitizeOutcome is what one walk produced.
type sanitizeOutcome struct {
	// Value is the sanitised document. It shares no map or slice with the input.
	Value any

	// Dropped is how many keys were removed, over the whole document.
	Dropped int

	// Truncated reports that a bound cut the walk, so the value is not the whole
	// document even once the drops are accounted for.
	Truncated bool
}

// sanitizeUntyped strips person- and place-identifying keys out of an untyped Garmin
// value, recursively, and reports how many it removed.
//
// This is where the honesty of the passthrough tools lives. Several wellness documents
// have no field set this project can source: upstream curates none of them, and no
// sample has shown one populated. Inventing Go field names for them would be a
// fabricated schema, so those tools hand the document on under the names Garmin
// actually used. That is the right call for typing and the wrong call for privacy,
// because whatever Garmin sends — an account identifier, a coordinate, a field that
// does not exist yet — would otherwise leave this server unread and unbounded.
//
// Running every such value through this function is what makes the two compatible: the
// caller still gets the structure Garmin actually sent, and the identifiers are gone.
// A tool that returns an untyped Garmin value and does not call this function is a
// defect.
//
// Removals are reported as a count and never as names. A key name is itself a
// disclosure: a drifted document could name a key after the health domain it belongs
// to, and a result or a log line that listed the removed names would carry exactly the
// account fact the removal was meant to withhold. The count tells a caller the object
// is incomplete, which is all a caller needs to know.
func sanitizeUntyped(value any) sanitizeOutcome {
	walker := &sanitizer{}
	out := walker.walk(value, 0)
	return sanitizeOutcome{Value: out, Dropped: walker.dropped, Truncated: walker.truncated}
}

// sanitizeRaw decodes a retained raw Garmin sub-object and sanitises it in one step. It
// is the sanitising replacement for decodeRaw on any path that reaches a result.
func sanitizeRaw(raw []byte) sanitizeOutcome {
	return sanitizeUntyped(decodeRaw(raw))
}

// untypedList is one bounded, sanitised list of untyped Garmin values.
type untypedList struct {
	// Values are the retained, sanitised elements, rendered as plain JSON data so
	// the published output schema describes them as open values, not byte arrays.
	Values []any

	// Truncated reports that the result is not the whole list: it was cut at the
	// limit, or an element was too deep or too wide for the sanitising walk.
	Truncated bool

	// Dropped is how many identifying keys were removed, over the whole list.
	Dropped int
}

// boundedUntyped cuts a raw Garmin list at limit and sanitises what it keeps.
//
// Cutting is right for a list and refusing is right for a single document: a list is
// ordered, so a caller told it was cut knows exactly what it holds and what it is
// missing, which is not true of half an object. Every uncurated list read shares this
// function, so the egress rule is written once — an element shape nobody can source is
// exactly the shape nobody can vet.
func boundedUntyped(raw []json.RawMessage, limit int) untypedList {
	out := untypedList{}
	if len(raw) > limit {
		raw = raw[:limit]
		out.Truncated = true
	}

	out.Values = make([]any, 0, len(raw))
	for _, element := range raw {
		outcome := sanitizeRaw(element)
		out.Dropped += outcome.Dropped
		out.Truncated = out.Truncated || outcome.Truncated
		if outcome.Value != nil {
			out.Values = append(out.Values, outcome.Value)
		}
	}
	return out
}

// sanitizer carries the running state of one walk.
type sanitizer struct {
	dropped   int
	nodes     int
	truncated bool
}

// walk copies value, dropping identifying keys, until a bound stops it.
func (s *sanitizer) walk(value any, depth int) any {
	s.nodes++
	if depth > maxSanitizeDepth || s.nodes > maxSanitizeNodes {
		s.truncated = true
		return nil
	}

	switch typed := value.(type) {
	case map[string]any:
		return s.walkObject(typed, depth)
	case []any:
		out := make([]any, 0, len(typed))
		for _, element := range typed {
			out = append(out, s.walk(element, depth+1))
		}
		return out
	default:
		return value
	}
}

// walkObject copies one object, dropping the keys that identify a person or a place.
func (s *sanitizer) walkObject(object map[string]any, depth int) map[string]any {
	out := make(map[string]any, len(object))
	for key, element := range object {
		if isIdentifyingKey(key) {
			s.dropped++
			continue
		}
		out[key] = s.walk(element, depth+1)
	}
	return out
}

// isIdentifyingKey reports whether a key names a person or a place.
func isIdentifyingKey(key string) bool {
	folded := strings.ToLower(key)
	if _, found := identifyingKeys[folded]; found {
		return true
	}
	for _, fragment := range identifyingFragments {
		if strings.Contains(folded, fragment) {
			return true
		}
	}
	return false
}

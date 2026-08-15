package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// Garmin's GraphQL tier.
//
// Source: python-garminconnect 0.3.10. GarminConnect.__init__ sets
// garmin_graphql_endpoint = "graphql-gateway/graphql", and query_garmin_graphql
// posts the request body to it through the same authenticated "connectapi" client
// every REST read uses:
//
//	self.client.post("connectapi", self.garmin_graphql_endpoint, json=query).json()
//
// No extra header, cookie or token is involved: the DI bearer the REST tier already
// carries is the whole authentication, which is why this package can reproduce the
// call at all.
//
// The three upstream callers are in garmin_mcp at commit 3610be6, and none of them
// sends an operationName or a GraphQL "variables" object. Each sends one anonymous
// query naming one scalar root field, with its arguments written into the query text
// as quoted string literals:
//
//	query{workoutScheduleSummariesScalar(startDate:"…", endDate:"…")}
//	query{trainingPlanScalar(calendarDate:"…", lang:"en-US", firstDayOfWeek:"monday")}
//
// That literal interpolation is reproduced here because it is what the gateway is
// known to accept; a variables-based rewrite would be a guess. Interpolation is only
// safe under a closed set and a strict charset, so GraphQLRequest supplies both: the
// root field must be one of this package's constants, and an argument value that
// could close its literal, escape it or open a second field is refused rather than
// escaped.
const (
	// PathGraphQL is the gateway path on the connectapi host.
	// Source: garmin_graphql_endpoint.
	PathGraphQL = "/graphql-gateway/graphql"

	// MaxGraphQLArguments bounds how many arguments one root field may take. The
	// widest upstream caller sends three.
	MaxGraphQLArguments = 8

	// MaxGraphQLArgumentValueLen bounds one argument value. Every value upstream
	// sends is a date or a short enumeration token.
	MaxGraphQLArgumentValueLen = 64

	// MaxGraphQLArgumentNameLen bounds one argument name.
	MaxGraphQLArgumentNameLen = 32

	// MaxGraphQLDocumentBytes bounds the rendered query document, which is the
	// request-body bound for this tier. The bounds on the response are the ones the
	// request layer already enforces for every read: MaxResponseBytes on the wire
	// and MaxDecompressedBytes after gunzip.
	MaxGraphQLDocumentBytes = 1024
)

// Argument names the upstream callers send. Source: the three call sites in
// garmin_mcp src/garmin_mcp/workouts.py and src/garmin_mcp/workout_builders.py.
const (
	GraphQLArgStartDate      = "startDate"
	GraphQLArgEndDate        = "endDate"
	GraphQLArgCalendarDate   = "calendarDate"
	GraphQLArgLang           = "lang"
	GraphQLArgFirstDayOfWeek = "firstDayOfWeek"
)

// Argument values the training-plan caller hard-codes upstream.
const (
	// GraphQLLangDefault is the lang upstream sends. It selects the language of the
	// plan text Garmin returns and names no user.
	GraphQLLangDefault = "en-US"
	// GraphQLFirstDayOfWeekDefault is the week anchor upstream sends.
	GraphQLFirstDayOfWeekDefault = "monday"
)

// GraphQLField is a sanitized GraphQL root field label.
//
// Like Endpoint and Op it is a closed set: only the constants below are labels, and
// a value built from any other string — a document, a URL, a caller argument —
// renders as "unknown" and is refused before dispatch. Every constant names a query
// field. There is no mutation field here, and that is what makes the request layer's
// read classification of this tier true rather than merely convenient.
type GraphQLField string

// The root fields this package queries.
const (
	// GraphQLFieldWorkoutScheduleSummaries is the workout calendar between two
	// dates. Source: get_scheduled_workouts and _is_already_scheduled.
	GraphQLFieldWorkoutScheduleSummaries = GraphQLField("workoutScheduleSummariesScalar")
	// GraphQLFieldTrainingPlan is the Garmin Coach / training-plan window around one
	// date. Source: _get_garmin_coach_workouts.
	GraphQLFieldTrainingPlan = GraphQLField("trainingPlanScalar")
)

var knownGraphQLFields = [...]GraphQLField{
	GraphQLFieldWorkoutScheduleSummaries,
	GraphQLFieldTrainingPlan,
}

// KnownGraphQLFields returns a copy of the root fields this package can query.
func KnownGraphQLFields() []GraphQLField {
	out := make([]GraphQLField, len(knownGraphQLFields))
	copy(out, knownGraphQLFields[:])
	return out
}

// IsKnown reports whether f is one of this package's GraphQLField constants.
func (f GraphQLField) IsKnown() bool {
	for _, known := range knownGraphQLFields {
		if f == known {
			return true
		}
	}
	return false
}

// String returns the label, or "unknown" for a value that is not a package constant.
func (f GraphQLField) String() string {
	if !f.IsKnown() {
		return labelUnknown
	}
	return string(f)
}

// GraphQLArgument is one root-field argument, rendered as a quoted string literal.
// Every argument upstream sends is a string, so there is no numeric or enum form to
// reproduce.
type GraphQLArgument struct {
	// Name is the argument name. It must be a GraphQL identifier.
	Name string
	// Value is the argument value. It is written into the query text between
	// quotes, so its charset is deliberately narrow.
	Value string
}

// GraphQLRequest is one query against Garmin's GraphQL tier, fully specified and
// validated before anything is rendered or dispatched.
//
// It is the GraphQL counterpart of Request and carries the same sanitized labels, so
// a failure on this tier logs and renders exactly like a REST failure.
type GraphQLRequest struct {
	// Op is the sanitized operation label. It must be a recognized label.
	Op Op
	// Endpoint is the sanitized endpoint label. It must be a recognized label.
	Endpoint Endpoint
	// Field is the root field to query. It must be a recognized label.
	Field GraphQLField
	// Arguments are the root field's arguments, rendered in order.
	Arguments []GraphQLArgument
}

// Validate reports whether the request may be rendered and dispatched. Every failure
// matches ErrValidation and names the rule, never the rejected value.
func (r GraphQLRequest) Validate() error {
	switch {
	case !r.Op.IsKnown():
		return validationError("graphql request op is not a recognized label")
	case !r.Endpoint.IsKnown():
		return validationError("graphql request endpoint is not a recognized label")
	case !r.Field.IsKnown():
		return validationError("graphql root field is not a recognized label")
	case len(r.Arguments) == 0:
		return validationError("a graphql request needs at least one argument")
	case len(r.Arguments) > MaxGraphQLArguments:
		return validationError("a graphql request carries more arguments than this package renders")
	}
	for _, argument := range r.Arguments {
		if err := argument.validate(); err != nil {
			return err
		}
	}
	return nil
}

// validate enforces the identifier rule on the name and the narrow literal charset on
// the value.
func (a GraphQLArgument) validate() error {
	if a.Name == "" || len(a.Name) > MaxGraphQLArgumentNameLen || !isGraphQLIdentifier(a.Name) {
		return validationError("graphql argument name must be a bounded GraphQL identifier")
	}
	if a.Value == "" || len(a.Value) > MaxGraphQLArgumentValueLen {
		return validationError("graphql argument value must be present and bounded")
	}
	for _, r := range a.Value {
		if !isGraphQLValueRune(r) {
			return validationError(
				"graphql argument value must be letters, digits, '-', '_', '.', ':' or '+'")
		}
	}
	return nil
}

// isGraphQLIdentifier reports whether name matches /[A-Za-z_][A-Za-z0-9_]*/, which is
// GraphQL's own name production.
func isGraphQLIdentifier(name string) bool {
	for index, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		case index > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}

// isGraphQLValueRune reports whether r may appear in an interpolated value. The set is
// an allowlist wide enough for a date, a locale tag and a weekday token, and narrow
// enough that no value can carry a quote, a backslash, a brace, a parenthesis, a
// comma, whitespace or a control character.
func isGraphQLValueRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	case r == '-', r == '_', r == '.', r == ':', r == '+':
		return true
	default:
		return false
	}
}

// Document renders the query text. It validates first, so an unrenderable request
// produces an error rather than a partial document.
func (r GraphQLRequest) Document() (string, error) {
	if err := r.Validate(); err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString("query{")
	b.WriteString(string(r.Field))
	b.WriteString("(")
	for index, argument := range r.Arguments {
		if index > 0 {
			b.WriteString(", ")
		}
		b.WriteString(argument.Name)
		b.WriteString(`:"`)
		b.WriteString(argument.Value)
		b.WriteString(`"`)
	}
	b.WriteString(")}")

	document := b.String()
	if len(document) > MaxGraphQLDocumentBytes {
		return "", validationError("graphql document exceeds its bound")
	}
	return document, nil
}

// graphQLBody is the request body upstream posts: the document under "query", and
// nothing else. No operationName and no variables object are sent, because no upstream
// caller sends either.
type graphQLBody struct {
	Query string `json:"query"`
}

// request renders the GraphQL request as the ordinary Request the dispatch path takes.
//
// The effect is EffectQueryRead: the document travels in a body, so the call is a
// POST, but it is a read. See EffectQueryRead for why repeating it is safe.
func (r GraphQLRequest) request() (Request, error) {
	document, err := r.Document()
	if err != nil {
		return Request{}, err
	}
	body, err := json.Marshal(graphQLBody{Query: document})
	if err != nil {
		return Request{}, validationError("graphql document could not be encoded")
	}
	return Request{
		Op:       r.Op,
		Endpoint: r.Endpoint,
		Method:   http.MethodPost,
		Path:     PathGraphQL,
		Body:     body,
		Effect:   EffectQueryRead,
	}, nil
}

// graphQLEnvelope is the tolerant response envelope.
//
// Garmin can answer 200 with a partial or absent data object and a populated errors
// array, so both are decoded and the errors decide the outcome. An error entry is
// kept as a raw message and never read: its text is Garmin's prose about the caller's
// own calendar, and this package retains none of it.
type graphQLEnvelope struct {
	Data   map[string]json.RawMessage `json:"data"`
	Errors []json.RawMessage          `json:"errors"`
}

// GraphQL performs req for the session's principal and decodes the queried root field
// into out.
//
// out receives data[field], which is what every upstream caller reads. Decoding is
// tolerant in the same one direction DecodeJSON is: an unknown sibling field, an
// absent data object and a null root field all leave out untouched and return nil,
// while a body that is not a JSON envelope is ErrMalformedPayload.
//
// A response carrying a non-empty errors array is a failure, whatever the HTTP status
// was and whatever data came with it. Reporting it as success would hand a caller an
// empty calendar as though Garmin had confirmed the calendar is empty. The failure
// matches ErrGraphQLErrors and carries the error count only.
//
// The returned Payload is the bounded, sealed response, as with every other call.
func (c *Client) GraphQL(
	ctx context.Context, session Session, req GraphQLRequest, out any,
) (Payload, error) {
	dispatch, err := req.request()
	if err != nil {
		return Payload{}, &APIError{
			Op: req.Op, Endpoint: req.Endpoint, Kind: KindValidation, Err: err,
		}
	}

	payload, err := c.Do(ctx, session, dispatch)
	if err != nil {
		return payload, err
	}

	var envelope graphQLEnvelope
	if err := DecodeJSON(payload, &envelope); err != nil {
		return payload, err
	}
	if count := len(envelope.Errors); count > 0 {
		return payload, &APIError{
			Op:       dispatch.Op,
			Endpoint: dispatch.Endpoint,
			Status:   payload.Status(),
			Kind:     KindUnknown,
			Err:      fmt.Errorf("%w: %s reported", ErrGraphQLErrors, strconv.Itoa(count)),
		}
	}
	return payload, decodeGraphQLField(payload, envelope, req.Field, out)
}

// decodeGraphQLField decodes data[field] into out, tolerating every absent form.
func decodeGraphQLField(
	payload Payload, envelope graphQLEnvelope, field GraphQLField, out any,
) error {
	if out == nil {
		return nil
	}
	raw, present := envelope.Data[string(field)]
	if !present || len(raw) == 0 || string(raw) == jsonNull {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return decodeFailure(payload, err)
	}
	return nil
}

package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Security audit events.
//
// # What may be recorded, and how that is enforced
//
// The table has exactly six columns and none of them is free text. kind and detail
// must match a reason-code grammar — lowercase letters, digits, underscore and dot,
// bounded length — which is checked before the insert and refuses anything else with
// ErrInvalidAuditDetail. That is the enforcement mechanism, not a convention: a token,
// a JWT, an email address, a URL, a coordinate pair and a JSON health payload all fail
// the grammar, because every one of them needs a character the grammar rejects.
//
// principal_id and client_id are internal identifiers, so an event names who without
// naming a mailbox. There is deliberately no column for a request body, a header, a
// query string, a Garmin response, a device id, a heart rate, a weight, a latitude or
// a longitude. If a future event needs more context, it gets a new reason code, not a
// free-text column.

// maxAuditKindLength and maxAuditDetailLength bound the two code columns.
const (
	maxAuditKindLength   = 64
	maxAuditDetailLength = 64
)

// maxAuditPageSize bounds one AuditEvents read, so a diagnostic call cannot pull the
// whole table into memory.
const maxAuditPageSize = 500

// AuditOutcome is the decision an event records. The schema constrains the column to
// these three values, so an unknown outcome cannot be stored.
type AuditOutcome string

// The three outcomes.
const (
	// AuditAllowed means the operation went ahead.
	AuditAllowed AuditOutcome = "allowed"

	// AuditDenied means policy refused it. This is the interesting one: a run of
	// denials is what an operator watches for.
	AuditDenied AuditOutcome = "denied"

	// AuditError means the operation failed for a reason that was not a policy
	// decision.
	AuditError AuditOutcome = "error"
)

// valid reports whether the outcome is one the schema accepts.
func (o AuditOutcome) valid() bool {
	switch o {
	case AuditAllowed, AuditDenied, AuditError:
		return true
	}
	return false
}

// AuditEvent is one security-relevant decision.
//
// Kind and Detail must be reason codes, matching [a-z][a-z0-9_.]*. Detail may be
// empty. PrincipalID and ClientID may be empty when the event happened before either
// was known.
type AuditEvent struct {
	// OccurredAt is when the decision was made. The zero value means "now",
	// according to the store's clock.
	OccurredAt time.Time

	// Kind is the event type, for example "token.issued" or "consent.revoked".
	Kind string

	// Outcome is the decision.
	Outcome AuditOutcome

	PrincipalID string
	ClientID    string

	// Detail is an optional reason code, for example "refresh_token_reuse".
	Detail string
}

// RecordAuditEvent appends one event.
//
// It reports ErrInvalidAuditDetail when Kind or Detail is not a reason code. The
// refusal is deliberate rather than a silent truncation: a caller that tried to log a
// credential must find out, because the same call site will try again.
//
// The insert carries no foreign key, so an event about a principal that has since been
// deleted survives. An audit record a cascade could erase would be worth very little.
func (s *SQLiteStore) RecordAuditEvent(ctx context.Context, event AuditEvent) error {
	if err := checkStoreRequest(ctx); err != nil {
		return err
	}
	if err := checkAuditEvent(event); err != nil {
		return err
	}

	occurredAt := event.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = s.now()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO audit_events (occurred_at, kind, outcome, principal_id, client_id, detail)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		formatTime(occurredAt), event.Kind, string(event.Outcome),
		nullableString(event.PrincipalID), nullableString(event.ClientID), event.Detail)
	if err != nil {
		return fmt.Errorf("store: record audit event: %w", err)
	}
	return nil
}

// checkAuditEvent applies the whole grammar.
func checkAuditEvent(event AuditEvent) error {
	if !event.Outcome.valid() {
		return fmt.Errorf("store: audit outcome %q is not allowed, want allowed, denied or error: %w",
			event.Outcome, ErrInvalidArgument)
	}
	if err := checkReasonCodeField("audit kind", event.Kind, maxAuditKindLength, false); err != nil {
		return err
	}
	err := checkReasonCodeField("audit detail", event.Detail, maxAuditDetailLength, true)
	if err != nil {
		return err
	}
	for kind, value := range map[string]string{
		"audit principal id": event.PrincipalID, "audit client id": event.ClientID,
	} {
		if len(value) > maxIdentifierLength {
			return fmt.Errorf("store: %s has length %d: %w", kind, len(value), ErrInvalidArgument)
		}
	}
	return nil
}

// checkReasonCodeField requires [a-z][a-z0-9_.]* within the bound. Dots are allowed so
// an event kind can be namespaced ("token.issued"); everything a credential or a
// payload needs — uppercase, '+', '/', '=', '@', ':', '{', a space, a newline — is not.
func checkReasonCodeField(field, value string, bound int, optional bool) error {
	if value == "" {
		if optional {
			return nil
		}
		return fmt.Errorf("store: empty %s: %w", field, ErrInvalidAuditDetail)
	}
	if len(value) > bound {
		return fmt.Errorf("store: %s has length %d, over the %d bound: %w",
			field, len(value), bound, ErrInvalidAuditDetail)
	}
	if first := value[0]; first < 'a' || first > 'z' {
		return fmt.Errorf("store: %s %q must start with a lowercase letter: %w",
			field, value, ErrInvalidAuditDetail)
	}
	if strings.HasSuffix(value, "_") || strings.HasSuffix(value, ".") {
		return fmt.Errorf("store: %s %q ends with a separator: %w", field, value, ErrInvalidAuditDetail)
	}
	return checkReasonCodeChars(field, value)
}

func checkReasonCodeChars(field, value string) error {
	for _, char := range value {
		lower := char >= 'a' && char <= 'z'
		digit := char >= '0' && char <= '9'
		if !lower && !digit && char != '_' && char != '.' {
			return fmt.Errorf(
				"store: %s %q is not a reason code, so it may carry a credential or a payload: %w",
				field, value, ErrInvalidAuditDetail)
		}
	}
	return nil
}

// AuditEvents returns the most recent events, newest first, at most limit of them.
// Zero selects the page-size cap, and a value over it is refused.
func (s *SQLiteStore) AuditEvents(ctx context.Context, limit int) ([]AuditEvent, error) {
	if err := checkStoreRequest(ctx); err != nil {
		return nil, err
	}
	if limit < 0 || limit > maxAuditPageSize {
		return nil, fmt.Errorf("store: audit page size %d is outside [0, %d]: %w",
			limit, maxAuditPageSize, ErrInvalidArgument)
	}
	if limit == 0 {
		limit = maxAuditPageSize
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT occurred_at, kind, outcome, principal_id, client_id, detail
		   FROM audit_events ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: read audit events: %w", err)
	}
	defer func() { _ = rows.Close() }()

	events := []AuditEvent{}
	for rows.Next() {
		event, err := scanAuditEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate audit events: %w", err)
	}
	return events, nil
}

func scanAuditEvent(row rowScanner) (AuditEvent, error) {
	var (
		event        AuditEvent
		occurredText string
		outcome      string
		principalID  sql.NullString
		clientID     sql.NullString
	)
	err := row.Scan(&occurredText, &event.Kind, &outcome, &principalID, &clientID, &event.Detail)
	if err != nil {
		return AuditEvent{}, fmt.Errorf("store: scan audit event: %w", err)
	}
	if event.OccurredAt, err = parseTime(occurredText); err != nil {
		return AuditEvent{}, err
	}
	event.Outcome = AuditOutcome(outcome)
	event.PrincipalID = principalID.String
	event.ClientID = clientID.String
	return event, nil
}
